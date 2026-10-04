package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iamnikolie/gengoya/internal/client"
	"github.com/iamnikolie/gengoya/internal/registry"
)

// VideoRequest is a provider-agnostic video generation request.
type VideoRequest struct {
	Model          registry.Model
	Prompt         string
	NegativePrompt string

	Resolution       string // 720p|1080p|4k; "" → omit
	Aspect           string // 16:9|9:16; "" → omit
	Duration         string // seconds as a string; "" → omit
	PersonGeneration string // allow_all|allow_adult; "" → omit

	// Image inputs. Image is the first frame (image-to-video); LastFrame closes
	// an interpolation; RefImages are style/content assets.
	Image         []byte
	ImageName     string
	LastFrame     []byte
	LastFrameName string
	RefImages     [][]byte
	RefImageNames []string

	// Omni stateful editing: an interaction id to continue from, or the bytes
	// of a local video (<=10s) to edit/extend. Veo rejects both.
	PreviousInteraction string
	EditVideo           []byte
	EditVideoName       string

	// Detach asks Omni to run in the background (poll later) instead of
	// holding one blocking request. Veo is always async and ignores it.
	Detach bool

	// Delivery overrides Omni's inline|uri choice; "" = automatic.
	Delivery string
}

// Video is one returned video payload.
type Video struct {
	Data     []byte
	MimeType string
	URI      string
}

// VideoResult is the finished video job.
type VideoResult struct {
	Videos        []Video
	OperationName string
	CostUSD       float64
	CostSource    string
}

// VideoOperation is a decoded long-running operation state.
type VideoOperation struct {
	Name  string
	Done  bool
	URIs  []string
	Error string

	// Videos carries payloads delivered inline (Omni returns base64 in the
	// interaction itself); URIs still go through Download.
	Videos []Video
	// CostUSD/CostSource are set when the API reported usage (Omni); zero
	// means "use the registry estimate".
	CostUSD    float64
	CostSource string
}

// VideoProvider starts, polls and downloads long-running video jobs.
type VideoProvider interface {
	Start(ctx context.Context, r VideoRequest) (string, error)
	Poll(ctx context.Context, operation string) (VideoOperation, error)
	Wait(ctx context.Context, operation string, interval time.Duration, onTick func(elapsed time.Duration, op VideoOperation)) (VideoOperation, error)
	Download(ctx context.Context, uri string) (Video, error)
}

// Veo implements VideoProvider against the Gemini Veo predictLongRunning API.
type Veo struct {
	APIKey string
	HTTP   *client.Client
}

// NewVeo constructs a Veo video provider.
func NewVeo(apiKey string, httpClient *client.Client) *Veo {
	if httpClient == nil {
		httpClient = client.New()
	}
	return &Veo{APIKey: apiKey, HTTP: httpClient}
}

// veoImage is Veo's image payload. The published REST samples show
// {"inlineData":{...}}, but the live API rejects that ("`inlineData` isn't
// supported by this model") — it wants the predict-style flat encoding.
// Verified 2026-08-02.
type veoImage struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
	MimeType           string `json:"mimeType"`
}

type veoReference struct {
	Image         veoImage `json:"image"`
	ReferenceType string   `json:"referenceType"`
}

type veoInstance struct {
	Prompt          string         `json:"prompt"`
	Image           *veoImage      `json:"image,omitempty"`
	LastFrame       *veoImage      `json:"lastFrame,omitempty"`
	ReferenceImages []veoReference `json:"referenceImages,omitempty"`
}

type veoRequest struct {
	Instances  []veoInstance  `json:"instances"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

func newVeoImage(data []byte, name string) *veoImage {
	if len(data) == 0 {
		return nil
	}
	if name == "" {
		name = "image.png"
	}
	return &veoImage{
		BytesBase64Encoded: base64.StdEncoding.EncodeToString(data),
		MimeType:           detectContentType(name),
	}
}

// BuildVeoRequest builds the predictLongRunning JSON body (pure, testable).
func BuildVeoRequest(r VideoRequest) ([]byte, error) {
	if strings.TrimSpace(r.Prompt) == "" && len(r.Image) == 0 {
		return nil, fmt.Errorf("provider.BuildVeoRequest: prompt or --image required")
	}
	if len(r.LastFrame) > 0 && len(r.Image) == 0 {
		return nil, fmt.Errorf("provider.BuildVeoRequest: --last-frame requires --image (first frame)")
	}

	inst := veoInstance{
		Prompt:    r.Prompt,
		Image:     newVeoImage(r.Image, r.ImageName),
		LastFrame: newVeoImage(r.LastFrame, r.LastFrameName),
	}
	for i, ref := range r.RefImages {
		name := ""
		if i < len(r.RefImageNames) {
			name = r.RefImageNames[i]
		}
		img := newVeoImage(ref, name)
		if img == nil {
			continue
		}
		inst.ReferenceImages = append(inst.ReferenceImages, veoReference{
			Image:         *img,
			ReferenceType: "asset",
		})
	}

	params := map[string]any{}
	if r.Aspect != "" {
		params["aspectRatio"] = r.Aspect
	}
	if r.Resolution != "" {
		params["resolution"] = strings.ToLower(r.Resolution)
	}
	if r.Duration != "" {
		// The REST docs show durationSeconds as a string; the live API rejects
		// that ("needs to be a number"). Send a number.
		secs, err := strconv.Atoi(strings.TrimSpace(r.Duration))
		if err != nil {
			return nil, fmt.Errorf("provider.BuildVeoRequest: duration %q is not an integer number of seconds", r.Duration)
		}
		params["durationSeconds"] = secs
	}
	if r.NegativePrompt != "" {
		params["negativePrompt"] = r.NegativePrompt
	}
	if r.PersonGeneration != "" {
		params["personGeneration"] = r.PersonGeneration
	}

	body := veoRequest{Instances: []veoInstance{inst}, Parameters: params}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildVeoRequest: %w", err)
	}
	return b, nil
}

type veoStartResponse struct {
	Name  string `json:"name"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ParseVeoStart extracts the operation name from a predictLongRunning response.
func ParseVeoStart(data []byte) (string, error) {
	var resp veoStartResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("provider.ParseVeoStart: %w (body: %s)", err, truncate(string(data), 300))
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", fmt.Errorf("provider.ParseVeoStart: %s", resp.Error.Message)
	}
	if resp.Name == "" {
		return "", fmt.Errorf("provider.ParseVeoStart: no operation name in response: %s", truncate(string(data), 300))
	}
	return resp.Name, nil
}

type veoVideoRef struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
}

type veoOperationResponse struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Metadata *struct {
		State string `json:"state"`
	} `json:"metadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Response *struct {
		GenerateVideoResponse *struct {
			GeneratedSamples []struct {
				Video veoVideoRef `json:"video"`
			} `json:"generatedSamples"`
			RaiMediaFilteredReasons []string `json:"raiMediaFilteredReasons"`
			RaiMediaFilteredCount   int      `json:"raiMediaFilteredCount"`
		} `json:"generateVideoResponse"`
		// Newer shape returned by some API revisions.
		GeneratedVideos []struct {
			Video veoVideoRef `json:"video"`
		} `json:"generatedVideos"`
		Videos []veoVideoRef `json:"videos"`
	} `json:"response"`
}

// ParseVeoOperation decodes an operation poll response (pure, testable).
// It tolerates the two documented response shapes for generated videos.
func ParseVeoOperation(data []byte) (VideoOperation, error) {
	var resp veoOperationResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return VideoOperation{}, fmt.Errorf("provider.ParseVeoOperation: %w (body: %s)", err, truncate(string(data), 300))
	}
	op := VideoOperation{Name: resp.Name, Done: resp.Done}
	if resp.Error != nil && resp.Error.Message != "" {
		op.Done = true
		op.Error = resp.Error.Message
		return op, nil
	}
	if resp.Response == nil {
		return op, nil
	}
	if gv := resp.Response.GenerateVideoResponse; gv != nil {
		for _, s := range gv.GeneratedSamples {
			if s.Video.URI != "" {
				op.URIs = append(op.URIs, s.Video.URI)
			}
		}
		if len(op.URIs) == 0 && gv.RaiMediaFilteredCount > 0 {
			op.Error = "all videos filtered by safety: " + strings.Join(gv.RaiMediaFilteredReasons, "; ")
		}
	}
	for _, s := range resp.Response.GeneratedVideos {
		if s.Video.URI != "" {
			op.URIs = append(op.URIs, s.Video.URI)
		}
	}
	for _, v := range resp.Response.Videos {
		if v.URI != "" {
			op.URIs = append(op.URIs, v.URI)
		}
	}
	if op.Done && len(op.URIs) == 0 && op.Error == "" {
		op.Error = "operation finished without a video URI: " + truncate(string(data), 300)
	}
	return op, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (v *Veo) startEndpoint(apiID string) string {
	return fmt.Sprintf("%s/models/%s:predictLongRunning", geminiBase, apiID)
}

// OperationURL builds the polling URL for an operation name or full URL.
func OperationURL(operation string) string {
	if strings.HasPrefix(operation, "http://") || strings.HasPrefix(operation, "https://") {
		return operation
	}
	return geminiBase + "/" + strings.TrimPrefix(operation, "/")
}

func (v *Veo) headers() map[string]string {
	return map[string]string{
		"x-goog-api-key": v.APIKey,
		"Content-Type":   "application/json",
	}
}

// Start submits the job and returns the operation name.
func (v *Veo) Start(ctx context.Context, r VideoRequest) (string, error) {
	body, err := BuildVeoRequest(r)
	if err != nil {
		return "", err
	}
	resp, _, err := v.HTTP.DoOnce(ctx, http.MethodPost, v.startEndpoint(r.Model.APIID), body, v.headers())
	if err != nil {
		return "", fmt.Errorf("provider.Veo.Start: %w", err)
	}
	return ParseVeoStart(resp)
}

// Poll fetches the operation state once.
func (v *Veo) Poll(ctx context.Context, operation string) (VideoOperation, error) {
	resp, _, err := v.HTTP.Do(ctx, http.MethodGet, OperationURL(operation), nil, map[string]string{
		"x-goog-api-key": v.APIKey,
	})
	if err != nil {
		return VideoOperation{}, fmt.Errorf("provider.Veo.Poll: %w", err)
	}
	op, err := ParseVeoOperation(resp)
	if err != nil {
		return VideoOperation{}, err
	}
	if op.Name == "" {
		op.Name = operation
	}
	return op, nil
}

// Wait polls until the operation is done, ctx is cancelled, or it fails.
// onTick, when non-nil, is called after each poll with the elapsed time.
func (v *Veo) Wait(ctx context.Context, operation string, interval time.Duration, onTick func(elapsed time.Duration, op VideoOperation)) (VideoOperation, error) {
	return waitLoop(ctx, "Veo", operation, interval, v.Poll, onTick)
}

func waitLoop(ctx context.Context, who, operation string, interval time.Duration,
	poll func(context.Context, string) (VideoOperation, error),
	onTick func(elapsed time.Duration, op VideoOperation)) (VideoOperation, error) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	start := time.Now()
	for {
		op, err := poll(ctx, operation)
		if err != nil {
			return VideoOperation{}, err
		}
		if onTick != nil {
			onTick(time.Since(start), op)
		}
		if op.Done {
			if op.Error != "" {
				return op, fmt.Errorf("provider.%s: operation failed: %s", who, op.Error)
			}
			return op, nil
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// DownloadURL normalizes a returned video URI into a fetchable URL.
func DownloadURL(uri string) string {
	if uri == "" {
		return ""
	}
	if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
		uri = geminiBase + "/" + strings.TrimPrefix(uri, "/")
	}
	if strings.Contains(uri, "alt=media") {
		return uri
	}
	if strings.Contains(uri, ":download") {
		sep := "?"
		if strings.Contains(uri, "?") {
			sep = "&"
		}
		return uri + sep + "alt=media"
	}
	return uri
}

// Download fetches the generated video bytes.
func (v *Veo) Download(ctx context.Context, uri string) (Video, error) {
	return downloadVideo(ctx, v.HTTP, v.APIKey, "Veo", uri)
}

func downloadVideo(ctx context.Context, hc *client.Client, apiKey, who, uri string) (Video, error) {
	url := DownloadURL(uri)
	data, contentType, err := hc.DoBinary(ctx, http.MethodGet, url, map[string]string{
		"x-goog-api-key": apiKey,
	})
	if err != nil {
		return Video{}, fmt.Errorf("provider.%s.Download: %w", who, err)
	}
	if len(data) == 0 {
		return Video{}, fmt.Errorf("provider.%s.Download: empty body from %s", who, url)
	}
	if strings.HasPrefix(contentType, "application/json") || strings.HasPrefix(contentType, "text/") {
		return Video{}, fmt.Errorf("provider.%s.Download: expected video, got %s: %s", who, contentType, truncate(string(data), 300))
	}
	mime := contentType
	if mime == "" {
		mime = "video/mp4"
	}
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return Video{Data: data, MimeType: mime, URI: uri}, nil
}

// Generate runs the whole job: start → wait → download.
func (v *Veo) Generate(ctx context.Context, r VideoRequest, interval time.Duration, onTick func(elapsed time.Duration, op VideoOperation)) (VideoResult, error) {
	name, err := v.Start(ctx, r)
	if err != nil {
		return VideoResult{}, err
	}
	op, err := v.Wait(ctx, name, interval, onTick)
	if err != nil {
		return VideoResult{OperationName: name}, err
	}
	res := VideoResult{OperationName: name}
	for _, uri := range op.URIs {
		vid, err := v.Download(ctx, uri)
		if err != nil {
			return res, err
		}
		res.Videos = append(res.Videos, vid)
	}
	res.CostUSD = registry.EstimateVideoCost(r.Model, r.Resolution, r.Duration)
	res.CostSource = CostSourceRegistry
	return res, nil
}

// VideoExtForMime maps a video mime type to a file extension without the dot.
// Anything that is not a recognised video/* subtype falls back to mp4 — a
// non-video content type must never leak into the filename.
func VideoExtForMime(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	switch m {
	case "video/mp4", "":
		return "mp4"
	case "video/webm":
		return "webm"
	case "video/quicktime":
		return "mov"
	}
	top, sub, ok := strings.Cut(m, "/")
	if ok && top == "video" && sub != "" && !strings.ContainsAny(sub, " ;/") {
		return sub
	}
	return "mp4"
}
