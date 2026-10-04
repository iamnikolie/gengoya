package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iamnikolie/gengoya/internal/client"
)

// Gemini Omni (gemini-omni-*) is not served by predictLongRunning: it uses the
// Interactions API (POST /v1beta/interactions). The "operation" gengoya tracks
// for an Omni job is "interactions/<id>"; the interaction id doubles as the
// handle for stateful edits (previous_interaction_id).

// OperationPrefixOmni marks an Omni operation name.
const OperationPrefixOmni = "interactions/"

// Token rates, USD per 1M tokens (pricing page, GA Standard tier, 2026-08).
const (
	omniPriceInPerM    = 1.50  // text/image/video/audio input
	omniPriceTextOutPM = 9.00  // text output incl. thinking
	omniPriceVideoOutM = 17.50 // video output
)

// IsOmniModel reports whether an api_id is served by the Interactions API.
func IsOmniModel(apiID string) bool {
	return strings.HasPrefix(apiID, "gemini-omni-")
}

// OmniInteractionID strips the operation prefix; bare ids pass through.
func OmniInteractionID(ref string) string {
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, OperationPrefixOmni)
}

// Omni implements VideoProvider against the Gemini Interactions API.
//
// By default a job is one blocking POST (background=false) whose response
// already carries the clip; Start parks it in done so Poll/Wait return it
// without a second request. --detach is refused: a background interaction
// (background+store) cannot be read back with an API key — GET
// /interactions/{id} answers "Multiple authentication credentials received"
// whether the key is sent as header or query (verified 2026-10-04), while
// GETs of non-background interactions work. Detaching would bill a clip that
// can never be downloaded.
type Omni struct {
	APIKey string
	HTTP   *client.Client

	// Base overrides the API root (tests); "" = the public Gemini endpoint.
	Base string

	mu   sync.Mutex
	done map[string]VideoOperation
}

// NewOmni constructs an Omni video provider.
func NewOmni(apiKey string, httpClient *client.Client) *Omni {
	if httpClient == nil {
		httpClient = client.New()
	}
	return &Omni{APIKey: apiKey, HTTP: httpClient}
}

type omniPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type omniResponseFormat struct {
	Type        string `json:"type"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	Duration    string `json:"duration,omitempty"`
	Delivery    string `json:"delivery,omitempty"`
}

type omniRequest struct {
	Model                 string             `json:"model"`
	Input                 any                `json:"input"`
	ResponseFormat        omniResponseFormat `json:"response_format"`
	PreviousInteractionID string             `json:"previous_interaction_id,omitempty"`
}

// omniDuration renders seconds as the proto Duration string the API accepts
// ("5s"). Bare numbers ("5", 5) are rejected with "Invalid input at
// 'response_format'" — verified live 2026-10-04.
func omniDuration(secs string) string {
	secs = strings.TrimSpace(secs)
	if secs == "" {
		return ""
	}
	return strings.TrimSuffix(secs, "s") + "s"
}

// omniDelivery picks inline (base64 in the response) or uri (a files/ handle).
// The docs cap inline payloads at 4MB and say to use uri above 720p, so 1080p
// and 4k default to uri; an explicit r.Delivery wins. "" omits the field.
func omniDelivery(r VideoRequest) string {
	if r.Delivery != "" {
		return r.Delivery
	}
	switch strings.ToLower(r.Resolution) {
	case "1080p", "4k":
		return "uri"
	}
	return ""
}

func omniMedia(kind string, data []byte, name, defaultName string) omniPart {
	if name == "" {
		name = defaultName
	}
	return omniPart{
		Type:     kind,
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: detectContentType(name),
	}
}

// BuildOmniRequest builds the POST /v1beta/interactions JSON body (pure).
//
// Input is a plain string for text-only prompts, otherwise a list of parts:
// media first (video, first frame, last frame, references), prompt last — the
// order of the docs' REST samples.
func BuildOmniRequest(r VideoRequest) ([]byte, error) {
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, fmt.Errorf("provider.BuildOmniRequest: a prompt is required")
	}
	if r.Detach {
		return nil, fmt.Errorf("provider.BuildOmniRequest: --detach is not supported by Omni: background interactions cannot be fetched with an API key, so the clip would be billed but never downloadable; run without --detach (a clip takes ~15-45s)")
	}
	if r.NegativePrompt != "" {
		return nil, fmt.Errorf("provider.BuildOmniRequest: --negative is not supported by Omni; put the negation in the prompt")
	}
	if r.PersonGeneration != "" {
		return nil, fmt.Errorf("provider.BuildOmniRequest: --person is Veo only")
	}
	if len(r.LastFrame) > 0 && len(r.Image) == 0 {
		return nil, fmt.Errorf("provider.BuildOmniRequest: --last-frame requires --image (first frame)")
	}
	if len(r.Image) > 0 && len(r.RefImages) > 0 {
		return nil, fmt.Errorf("provider.BuildOmniRequest: --image and --ref cannot be combined for Omni")
	}
	if len(r.EditVideo) > 0 && r.PreviousInteraction != "" {
		return nil, fmt.Errorf("provider.BuildOmniRequest: edit source is either a video file or an interaction id, not both")
	}

	if r.Delivery != "" && r.Delivery != "inline" && r.Delivery != "uri" {
		return nil, fmt.Errorf("provider.BuildOmniRequest: --delivery must be inline or uri, got %q", r.Delivery)
	}

	var parts []omniPart
	if len(r.EditVideo) > 0 {
		parts = append(parts, omniMedia("video", r.EditVideo, r.EditVideoName, "video.mp4"))
	}
	if len(r.Image) > 0 {
		parts = append(parts, omniMedia("image", r.Image, r.ImageName, "image.png"))
	}
	if len(r.LastFrame) > 0 {
		parts = append(parts, omniMedia("image", r.LastFrame, r.LastFrameName, "image.png"))
	}
	for i, ref := range r.RefImages {
		name := ""
		if i < len(r.RefImageNames) {
			name = r.RefImageNames[i]
		}
		parts = append(parts, omniMedia("image", ref, name, "image.png"))
	}

	req := omniRequest{
		Model: r.Model.APIID,
		ResponseFormat: omniResponseFormat{
			Type:        "video",
			AspectRatio: r.Aspect,
			Resolution:  strings.ToLower(r.Resolution),
			Duration:    omniDuration(r.Duration),
			Delivery:    omniDelivery(r),
		},
		PreviousInteractionID: OmniInteractionID(r.PreviousInteraction),
	}
	if len(parts) == 0 {
		req.Input = r.Prompt
	} else {
		req.Input = append(parts, omniPart{Type: "text", Text: r.Prompt})
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildOmniRequest: %w", err)
	}
	return b, nil
}

type omniTokens struct {
	Modality string `json:"modality"`
	Tokens   int    `json:"tokens"`
}

type omniUsage struct {
	TotalInputTokens   int          `json:"total_input_tokens"`
	TotalOutputTokens  int          `json:"total_output_tokens"`
	TotalThoughtTokens int          `json:"total_thought_tokens"`
	OutputByModality   []omniTokens `json:"output_tokens_by_modality"`
}

type omniInteraction struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Steps []struct {
		Type    string `json:"type"`
		Content []struct {
			Type     string `json:"type"`
			MimeType string `json:"mime_type"`
			Data     string `json:"data"`
			URI      string `json:"uri"`
		} `json:"content"`
	} `json:"steps"`
	// SDK-style convenience field; the docs say REST has none, but tolerate it.
	OutputVideo *struct {
		MimeType string `json:"mime_type"`
		Data     string `json:"data"`
		URI      string `json:"uri"`
	} `json:"output_video"`
	Usage *omniUsage `json:"usage"`
}

// OmniCostFromUsage prices an interaction from its reported token usage.
// Video output tokens bill at the video rate; remaining output (text and
// thinking) at the text rate. Returns ok=false when usage is absent.
func OmniCostFromUsage(u *omniUsage) (float64, bool) {
	if u == nil || (u.TotalInputTokens == 0 && u.TotalOutputTokens == 0) {
		return 0, false
	}
	video := 0
	for _, m := range u.OutputByModality {
		if strings.EqualFold(m.Modality, "video") {
			video += m.Tokens
		}
	}
	other := u.TotalOutputTokens - video
	if other < 0 {
		other = 0
	}
	other += u.TotalThoughtTokens
	usd := float64(u.TotalInputTokens)*omniPriceInPerM/1e6 +
		float64(video)*omniPriceVideoOutM/1e6 +
		float64(other)*omniPriceTextOutPM/1e6
	return usd, true
}

// ParseOmniInteraction decodes an interaction response (create or get) into a
// VideoOperation. Inline base64 videos land in Videos, URI deliveries in URIs.
func ParseOmniInteraction(data []byte) (VideoOperation, error) {
	var resp omniInteraction
	if err := json.Unmarshal(data, &resp); err != nil {
		return VideoOperation{}, fmt.Errorf("provider.ParseOmniInteraction: %w (body: %s)", err, truncate(string(data), 300))
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return VideoOperation{}, fmt.Errorf("provider.ParseOmniInteraction: %s", resp.Error.Message)
	}
	op := VideoOperation{Name: OperationPrefixOmni + resp.ID}
	if resp.ID == "" {
		op.Name = ""
	}
	switch resp.Status {
	case "completed", "incomplete":
		op.Done = true
	case "failed", "cancelled", "canceled":
		op.Done = true
		op.Error = "interaction " + resp.Status
	}

	for _, s := range resp.Steps {
		if s.Type != "model_output" {
			continue
		}
		for _, c := range s.Content {
			if c.Type != "video" {
				continue
			}
			mime := c.MimeType
			if mime == "" {
				mime = "video/mp4"
			}
			switch {
			case c.Data != "":
				raw, err := base64.StdEncoding.DecodeString(c.Data)
				if err != nil {
					return VideoOperation{}, fmt.Errorf("provider.ParseOmniInteraction: bad video base64: %w", err)
				}
				op.Videos = append(op.Videos, Video{Data: raw, MimeType: mime})
			case c.URI != "":
				op.URIs = append(op.URIs, c.URI)
			}
		}
	}
	if len(op.Videos) == 0 && len(op.URIs) == 0 && resp.OutputVideo != nil {
		ov := resp.OutputVideo
		if ov.Data != "" {
			raw, err := base64.StdEncoding.DecodeString(ov.Data)
			if err != nil {
				return VideoOperation{}, fmt.Errorf("provider.ParseOmniInteraction: bad video base64: %w", err)
			}
			mime := ov.MimeType
			if mime == "" {
				mime = "video/mp4"
			}
			op.Videos = append(op.Videos, Video{Data: raw, MimeType: mime})
		} else if ov.URI != "" {
			op.URIs = append(op.URIs, ov.URI)
		}
	}

	if op.Done && op.Error == "" && len(op.Videos) == 0 && len(op.URIs) == 0 {
		op.Error = "interaction " + resp.Status + " without a video (blocked by safety filters?): " + truncate(string(data), 300)
	}
	if usd, ok := OmniCostFromUsage(resp.Usage); ok && op.Done {
		op.CostUSD, op.CostSource = usd, CostSourceUsage
	}
	return op, nil
}

func (o *Omni) base() string {
	if o.Base != "" {
		return o.Base
	}
	return geminiBase
}

func (o *Omni) headers() map[string]string {
	return map[string]string{
		"x-goog-api-key": o.APIKey,
		"Content-Type":   "application/json",
	}
}

// Start creates the interaction and returns "interactions/<id>".
func (o *Omni) Start(ctx context.Context, r VideoRequest) (string, error) {
	body, err := BuildOmniRequest(r)
	if err != nil {
		return "", err
	}
	// One generation per call: never re-POST on 5xx (see client.DoOnce).
	resp, _, err := o.HTTP.DoOnce(ctx, http.MethodPost, o.base()+"/interactions", body, o.headers())
	if err != nil {
		if code := httpStatus(err); code >= 500 {
			return "", fmt.Errorf("provider.Omni.Start: %w (not retried: the server may already have generated and billed the clip — check AI Studio usage before re-running)", err)
		}
		return "", fmt.Errorf("provider.Omni.Start: %w", err)
	}
	op, err := ParseOmniInteraction(resp)
	if err != nil {
		return "", err
	}
	if op.Error != "" {
		return "", fmt.Errorf("provider.Omni.Start: %s%s", op.Error, billedNote(op))
	}
	if op.Name == "" {
		return "", fmt.Errorf("provider.Omni.Start: no interaction id in response%s: %s", billedNote(op), truncate(string(resp), 300))
	}
	if op.Done {
		o.mu.Lock()
		if o.done == nil {
			o.done = map[string]VideoOperation{}
		}
		o.done[op.Name] = op
		o.mu.Unlock()
	}
	return op.Name, nil
}

// Poll fetches the interaction once.
func (o *Omni) Poll(ctx context.Context, operation string) (VideoOperation, error) {
	o.mu.Lock()
	cached, ok := o.done[OperationPrefixOmni+OmniInteractionID(operation)]
	o.mu.Unlock()
	if ok {
		return cached, nil
	}
	url := o.base() + "/interactions/" + OmniInteractionID(operation)
	resp, _, err := o.HTTP.Do(ctx, http.MethodGet, url, nil, map[string]string{"x-goog-api-key": o.APIKey})
	if err != nil {
		if strings.Contains(err.Error(), "Multiple authentication credentials") || strings.Contains(err.Error(), "API_KEY_SERVICE_BLOCKED") {
			err = fmt.Errorf("%w (background interactions cannot be fetched with an API key; this clip is not downloadable)", err)
		}
		return VideoOperation{}, fmt.Errorf("provider.Omni.Poll: %w", err)
	}
	op, err := ParseOmniInteraction(resp)
	if err != nil {
		return VideoOperation{}, err
	}
	op.Name = OperationPrefixOmni + OmniInteractionID(operation)
	return op, nil
}

// Wait polls until the interaction is done, ctx is cancelled, or it fails.
func (o *Omni) Wait(ctx context.Context, operation string, interval time.Duration, onTick func(elapsed time.Duration, op VideoOperation)) (VideoOperation, error) {
	return waitLoop(ctx, "Omni", operation, interval, o.Poll, onTick)
}

// Download fetches a URI-delivered video (delivery=uri, the default at
// 1080p/4k), waiting for the file to become ACTIVE first.
func (o *Omni) Download(ctx context.Context, uri string) (Video, error) {
	o.waitActive(ctx, uri)
	return downloadVideo(ctx, o.HTTP, o.APIKey, "Omni", uri)
}

// waitActive polls a files/<id> handle until its state is ACTIVE (the docs say
// to wait before downloading). Best effort: any GET problem falls through to
// the download attempt, which reports its own error.
func (o *Omni) waitActive(ctx context.Context, uri string) {
	i := strings.Index(uri, "/files/")
	if i < 0 {
		return
	}
	name := strings.TrimPrefix(uri[i+1:], "/")
	if j := strings.IndexAny(name, ":?"); j >= 0 {
		name = name[:j]
	}
	for n := 0; n < 40; n++ {
		resp, _, err := o.HTTP.Do(ctx, http.MethodGet, o.base()+"/"+name, nil, map[string]string{"x-goog-api-key": o.APIKey})
		if err != nil {
			return
		}
		var f struct {
			State string `json:"state"`
		}
		if json.Unmarshal(resp, &f) != nil || f.State == "ACTIVE" || f.State == "FAILED" || f.State == "" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// GeminiVideo routes Gemini video work: Omni models and "interactions/..."
// operations go to the Interactions API, everything else to Veo.
type GeminiVideo struct {
	Veo  *Veo
	Omni *Omni
}

// NewGeminiVideo builds the router sharing one key and HTTP client.
func NewGeminiVideo(apiKey string, httpClient *client.Client) *GeminiVideo {
	return &GeminiVideo{Veo: NewVeo(apiKey, httpClient), Omni: NewOmni(apiKey, httpClient)}
}

func (g *GeminiVideo) forOperation(operation string) VideoProvider {
	if strings.HasPrefix(operation, OperationPrefixOmni) {
		return g.Omni
	}
	return g.Veo
}

func (g *GeminiVideo) Start(ctx context.Context, r VideoRequest) (string, error) {
	if IsOmniModel(r.Model.APIID) {
		return g.Omni.Start(ctx, r)
	}
	if r.PreviousInteraction != "" || len(r.EditVideo) > 0 {
		return "", fmt.Errorf("--edit is supported by Omni models only")
	}
	return g.Veo.Start(ctx, r)
}

func (g *GeminiVideo) Poll(ctx context.Context, operation string) (VideoOperation, error) {
	return g.forOperation(operation).Poll(ctx, operation)
}

func (g *GeminiVideo) Wait(ctx context.Context, operation string, interval time.Duration, onTick func(elapsed time.Duration, op VideoOperation)) (VideoOperation, error) {
	return g.forOperation(operation).Wait(ctx, operation, interval, onTick)
}

// Download waits for a files/ handle to turn ACTIVE before fetching it. Omni
// uri deliveries (the default at 1080p/4k) can still be PROCESSING when the
// interaction completes; for Veo files the check is one extra metadata GET.
func (g *GeminiVideo) Download(ctx context.Context, uri string) (Video, error) {
	g.Omni.waitActive(ctx, uri)
	return g.Veo.Download(ctx, uri)
}

// billedNote reports the usage-based cost of a call that produced no clip, so
// a failure that was still billed does not look free.
func billedNote(op VideoOperation) string {
	if op.CostUSD <= 0 {
		return ""
	}
	return fmt.Sprintf(" (billed ≈$%.2f per usage)", op.CostUSD)
}

// httpStatus extracts the code from a client "HTTP <code>: ..." error, or 0.
func httpStatus(err error) int {
	var code int
	if i := strings.Index(err.Error(), "HTTP "); i >= 0 {
		_, _ = fmt.Sscanf(err.Error()[i:], "HTTP %d", &code)
	}
	return code
}
