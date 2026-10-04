package provider

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/iamnikolie/gengoya/internal/registry"
)

// GenRequest is a provider-agnostic image generation or edit request.
type GenRequest struct {
	Model   registry.Model
	Prompt  string
	N       int
	Size    string   // "" or "auto" → omit / let provider default
	Aspect  string   // Gemini --aspect; ignored by OpenAI
	Quality string   // "" → omit
	Format  string   // png|jpeg|webp
	Images  [][]byte // edit only: input image bytes (0 for generate)
	Mask    []byte   // edit only, optional
	// ImageNames are filenames corresponding to Images (for multipart Content-Type).
	ImageNames []string

	// OpenAI extras
	Background  string // auto|transparent|opaque; empty = omit
	Compression int    // 0..100; -1 = unset
	Moderation  string // auto|low; empty = omit
}

// Image is one returned image payload.
type Image struct {
	Data     []byte
	Size     string // best-known "WxH" (measured or requested)
	MimeType string // e.g. image/png
}

// GenResult is the provider response.
type GenResult struct {
	Images        []Image
	RevisedPrompt string
	CostUSD       float64
	CostSource    string // "usage" | "registry"
	Usage         *Usage // OpenAI usage when present
}

// Provider generates and edits images.
type Provider interface {
	Generate(ctx context.Context, r GenRequest) (GenResult, error)
	Edit(ctx context.Context, r GenRequest) (GenResult, error) // r.Images non-empty
}

func costOf(r GenRequest, n int) float64 {
	return float64(n) * r.Model.PricePerImg
}

func resolveSize(r GenRequest) string {
	if r.Size == "" || r.Size == "auto" {
		return ""
	}
	return r.Size
}

func displaySize(r GenRequest) string {
	if r.Size == "" || r.Size == "auto" {
		return "auto"
	}
	return r.Size
}

// MeasureSize returns "WxH" from image bytes, or "" if undecodable.
func MeasureSize(data []byte) string {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
}

// EnrichImage fills MimeType/Size defaults after decode.
func EnrichImage(img *Image, fallbackMime, fallbackSize string) {
	if img.MimeType == "" {
		img.MimeType = fallbackMime
	}
	if measured := MeasureSize(img.Data); measured != "" {
		img.Size = measured
	} else if img.Size == "" {
		img.Size = fallbackSize
	}
}
