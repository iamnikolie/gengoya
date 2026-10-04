package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iamnikolie/gengoya/internal/client"
)

const geminiBase = "https://generativelanguage.googleapis.com/v1beta"

// Gemini implements Provider against the Gemini generateContent image API.
type Gemini struct {
	APIKey string
	HTTP   *client.Client
	// Base overrides the API root (tests); "" = the public Gemini endpoint.
	Base string
}

// NewGemini constructs a Gemini provider.
func NewGemini(apiKey string, httpClient *client.Client) *Gemini {
	if httpClient == nil {
		httpClient = client.New()
	}
	return &Gemini{APIKey: apiKey, HTTP: httpClient}
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiRequest struct {
	Contents         []geminiContent `json:"contents"`
	GenerationConfig map[string]any  `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// BuildGeminiRequest builds the generateContent JSON body (pure, testable).
func BuildGeminiRequest(r GenRequest) ([]byte, error) {
	parts := make([]geminiPart, 0, len(r.Images)+2)
	for i, img := range r.Images {
		name := "image.png"
		if i < len(r.ImageNames) && r.ImageNames[i] != "" {
			name = r.ImageNames[i]
		}
		parts = append(parts, geminiPart{
			InlineData: &geminiInlineData{
				MimeType: detectContentType(name),
				Data:     base64.StdEncoding.EncodeToString(img),
			},
		})
	}
	if len(r.Mask) > 0 {
		parts = append(parts, geminiPart{
			InlineData: &geminiInlineData{
				MimeType: "image/png",
				Data:     base64.StdEncoding.EncodeToString(r.Mask),
			},
		})
	}
	parts = append(parts, geminiPart{Text: r.Prompt})

	genCfg := map[string]any{
		"responseModalities": []string{"IMAGE", "TEXT"},
	}
	imgCfg, ok, err := MapGeminiParams(r.Model, r.Size, r.Aspect)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildGeminiRequest: %w", err)
	}
	if ok {
		cfg := map[string]any{}
		if imgCfg.AspectRatio != "" {
			cfg["aspectRatio"] = imgCfg.AspectRatio
		}
		if imgCfg.ImageSize != "" {
			cfg["imageSize"] = imgCfg.ImageSize
		}
		if len(cfg) > 0 {
			genCfg["imageConfig"] = cfg
		}
	}

	req := geminiRequest{
		Contents:         []geminiContent{{Parts: parts}},
		GenerationConfig: genCfg,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildGeminiRequest: %w", err)
	}
	return b, nil
}

// ParseGeminiResponse walks candidates[0].content.parts for inline images (pure, testable).
func ParseGeminiResponse(data []byte, r GenRequest) (GenResult, error) {
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return GenResult{}, fmt.Errorf("provider.ParseGeminiResponse: %w", err)
	}
	if len(resp.Candidates) == 0 {
		return GenResult{}, fmt.Errorf("provider.ParseGeminiResponse: no candidates")
	}
	fallbackSize := displaySize(r)
	out := GenResult{}
	for _, p := range resp.Candidates[0].Content.Parts {
		if p.InlineData != nil && p.InlineData.Data != "" {
			raw, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				return GenResult{}, fmt.Errorf("provider.ParseGeminiResponse: decode: %w", err)
			}
			mime := p.InlineData.MimeType
			if mime == "" {
				mime = "image/png"
			}
			img := Image{Data: raw, MimeType: mime, Size: fallbackSize}
			EnrichImage(&img, mime, fallbackSize)
			out.Images = append(out.Images, img)
			continue
		}
		if p.Text != "" && out.RevisedPrompt == "" {
			out.RevisedPrompt = p.Text
		}
	}
	if len(out.Images) == 0 {
		return GenResult{}, fmt.Errorf("provider.ParseGeminiResponse: no images in response")
	}
	out.CostUSD, out.CostSource = applyCost(r, len(out.Images), nil)
	return out, nil
}

// GeminiQualityNote returns a stderr note when quality was set.
func GeminiQualityNote(quality string) string {
	if quality == "" {
		return ""
	}
	return "note: --quality ignored for Gemini"
}

// GeminiFormatNote returns a stderr note that --format is not sent to Gemini.
func GeminiFormatNote(format string) string {
	if format == "" || format == "png" {
		return ""
	}
	return fmt.Sprintf("note: --format %q not sent to Gemini (file extension follows response mime)", format)
}

func (g *Gemini) endpoint(apiID string) string {
	base := g.Base
	if base == "" {
		base = geminiBase
	}
	return fmt.Sprintf("%s/models/%s:generateContent", base, apiID)
}

// Generate calls generateContent with a text prompt.
func (g *Gemini) Generate(ctx context.Context, r GenRequest) (GenResult, error) {
	return g.call(ctx, r)
}

// Edit calls generateContent with input image parts + prompt.
func (g *Gemini) Edit(ctx context.Context, r GenRequest) (GenResult, error) {
	if len(r.Images) == 0 {
		return GenResult{}, fmt.Errorf("provider.Gemini.Edit: at least one input image required")
	}
	return g.call(ctx, r)
}

func (g *Gemini) call(ctx context.Context, r GenRequest) (GenResult, error) {
	n := r.N
	if n < 1 {
		n = 1
	}
	// Gemini generateContent has no n parameter — loop for -n > 1.
	var out GenResult
	for i := 0; i < n; i++ {
		body, err := BuildGeminiRequest(r)
		if err != nil {
			return GenResult{}, err
		}
		resp, _, err := g.HTTP.DoOnce(ctx, http.MethodPost, g.endpoint(r.Model.APIID), body, map[string]string{
			"x-goog-api-key": g.APIKey,
			"Content-Type":   "application/json",
		})
		if err != nil {
			return GenResult{}, fmt.Errorf("provider.Gemini: %w", err)
		}
		part, err := ParseGeminiResponse(resp, r)
		if err != nil {
			return GenResult{}, err
		}
		out.Images = append(out.Images, part.Images...)
		if out.RevisedPrompt == "" {
			out.RevisedPrompt = part.RevisedPrompt
		}
	}
	out.CostUSD, out.CostSource = applyCost(r, len(out.Images), nil)
	return out, nil
}

// ExtForMime maps mime to a file extension without dot.
func ExtForMime(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	case "image/png":
		return "png"
	default:
		if strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg") {
			return "jpeg"
		}
		if strings.Contains(mime, "webp") {
			return "webp"
		}
		return "png"
	}
}
