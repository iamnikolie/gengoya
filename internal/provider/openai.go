package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"

	"github.com/iamnikolie/gengoya/internal/client"
)

const openaiBase = "https://api.openai.com/v1"

// OpenAI implements Provider against the OpenAI Images API.
type OpenAI struct {
	APIKey string
	HTTP   *client.Client
}

// NewOpenAI constructs an OpenAI provider.
func NewOpenAI(apiKey string, httpClient *client.Client) *OpenAI {
	if httpClient == nil {
		httpClient = client.New()
	}
	return &OpenAI{APIKey: apiKey, HTTP: httpClient}
}

type openaiGenBody struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 int    `json:"n,omitempty"`
	Size              string `json:"size,omitempty"`
	Quality           string `json:"quality,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	Background        string `json:"background,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
}

type openaiImageResp struct {
	Created int `json:"created"`
	Data    []struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage *Usage `json:"usage"`
}

// BuildOpenAIGenerateBody builds the JSON body for /images/generations (pure, testable).
func BuildOpenAIGenerateBody(r GenRequest) ([]byte, error) {
	if err := ValidateOpenAIExtras(r); err != nil {
		return nil, err
	}
	body := openaiGenBody{
		Model:  r.Model.APIID,
		Prompt: r.Prompt,
		N:      r.N,
	}
	if size := resolveSize(r); size != "" {
		body.Size = size
	}
	if r.Quality != "" {
		body.Quality = r.Quality
	}
	if r.Format != "" {
		body.OutputFormat = r.Format
	}
	if r.Background != "" {
		body.Background = r.Background
	}
	if r.Moderation != "" {
		body.Moderation = r.Moderation
	}
	if r.Compression >= 0 {
		c := r.Compression
		body.OutputCompression = &c
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildOpenAIGenerateBody: %w", err)
	}
	return b, nil
}

// ParseOpenAIResponse decodes a generations/edits JSON response (pure, testable).
func ParseOpenAIResponse(data []byte, r GenRequest) (GenResult, error) {
	var resp openaiImageResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return GenResult{}, fmt.Errorf("provider.ParseOpenAIResponse: %w", err)
	}
	if len(resp.Data) == 0 {
		return GenResult{}, fmt.Errorf("provider.ParseOpenAIResponse: empty data")
	}
	fallbackSize := displaySize(r)
	mimeType := formatMime(r.Format)
	out := GenResult{
		Images: make([]Image, 0, len(resp.Data)),
		Usage:  resp.Usage,
	}
	for _, d := range resp.Data {
		raw, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return GenResult{}, fmt.Errorf("provider.ParseOpenAIResponse: decode: %w", err)
		}
		img := Image{Data: raw, MimeType: mimeType, Size: fallbackSize}
		EnrichImage(&img, mimeType, fallbackSize)
		out.Images = append(out.Images, img)
		if out.RevisedPrompt == "" && d.RevisedPrompt != "" {
			out.RevisedPrompt = d.RevisedPrompt
		}
	}
	out.CostUSD, out.CostSource = applyCost(r, len(out.Images), resp.Usage)
	return out, nil
}

// BuildOpenAIEditMultipart builds multipart body for /images/edits (pure, testable).
func BuildOpenAIEditMultipart(r GenRequest) (body []byte, contentType string, err error) {
	if err := ValidateOpenAIExtras(r); err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fields := map[string]string{
		"model":  r.Model.APIID,
		"prompt": r.Prompt,
	}
	if r.N > 0 {
		fields["n"] = fmt.Sprintf("%d", r.N)
	}
	if size := resolveSize(r); size != "" {
		fields["size"] = size
	}
	if r.Quality != "" {
		fields["quality"] = r.Quality
	}
	if r.Format != "" {
		fields["output_format"] = r.Format
	}
	if r.Background != "" {
		fields["background"] = r.Background
	}
	if r.Moderation != "" {
		fields["moderation"] = r.Moderation
	}
	if r.Compression >= 0 {
		fields["output_compression"] = fmt.Sprintf("%d", r.Compression)
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, "", fmt.Errorf("provider.BuildOpenAIEditMultipart: %w", err)
		}
	}

	for i, img := range r.Images {
		name := "image.png"
		if i < len(r.ImageNames) && r.ImageNames[i] != "" {
			name = filepath.Base(r.ImageNames[i])
		}
		ct := detectContentType(name)
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="%s"`, name))
		h.Set("Content-Type", ct)
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", fmt.Errorf("provider.BuildOpenAIEditMultipart: %w", err)
		}
		if _, err := part.Write(img); err != nil {
			return nil, "", fmt.Errorf("provider.BuildOpenAIEditMultipart: %w", err)
		}
	}

	if len(r.Mask) > 0 {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="mask"; filename="mask.png"`)
		h.Set("Content-Type", "image/png")
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", fmt.Errorf("provider.BuildOpenAIEditMultipart: %w", err)
		}
		if _, err := part.Write(r.Mask); err != nil {
			return nil, "", fmt.Errorf("provider.BuildOpenAIEditMultipart: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("provider.BuildOpenAIEditMultipart: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func detectContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// Generate calls POST /v1/images/generations.
func (o *OpenAI) Generate(ctx context.Context, r GenRequest) (GenResult, error) {
	body, err := BuildOpenAIGenerateBody(r)
	if err != nil {
		return GenResult{}, err
	}
	resp, _, err := o.HTTP.DoOnce(ctx, http.MethodPost, openaiBase+"/images/generations", body, map[string]string{
		"Authorization": "Bearer " + o.APIKey,
		"Content-Type":  "application/json",
	})
	if err != nil {
		return GenResult{}, fmt.Errorf("provider.OpenAI.Generate: %w", err)
	}
	return ParseOpenAIResponse(resp, r)
}

// Edit calls POST /v1/images/edits.
func (o *OpenAI) Edit(ctx context.Context, r GenRequest) (GenResult, error) {
	if len(r.Images) == 0 {
		return GenResult{}, fmt.Errorf("provider.OpenAI.Edit: at least one input image required")
	}
	body, ct, err := BuildOpenAIEditMultipart(r)
	if err != nil {
		return GenResult{}, err
	}
	resp, _, err := o.HTTP.DoMultipart(ctx, http.MethodPost, openaiBase+"/images/edits", body, ct, map[string]string{
		"Authorization": "Bearer " + o.APIKey,
	})
	if err != nil {
		return GenResult{}, fmt.Errorf("provider.OpenAI.Edit: %w", err)
	}
	return ParseOpenAIResponse(resp, r)
}

func formatMime(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
