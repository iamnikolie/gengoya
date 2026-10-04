package provider

import (
	"fmt"
	"strings"

	"github.com/iamnikolie/gengoya/internal/registry"
)

// GeminiImageConfig is the generationConfig.imageConfig payload.
type GeminiImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
}

// MapGeminiParams maps CLI --size / --aspect into Gemini imageConfig fields.
// Returns ok=false when both should be omitted (auto + no aspect → API default).
func MapGeminiParams(m registry.Model, size, aspect string) (cfg GeminiImageConfig, ok bool, err error) {
	size = strings.TrimSpace(size)
	aspect = strings.TrimSpace(aspect)

	if aspect != "" {
		if err := registry.ValidateAspect(m, aspect); err != nil {
			return GeminiImageConfig{}, false, err
		}
	}

	// --aspect alone → aspect + default 1K (or first allowed)
	if (size == "" || strings.EqualFold(size, "auto")) && aspect != "" {
		imgSize := pickDefaultImageSize(m)
		return GeminiImageConfig{AspectRatio: aspect, ImageSize: imgSize}, true, nil
	}

	if size == "" || strings.EqualFold(size, "auto") {
		return GeminiImageConfig{}, false, nil
	}

	if token, isToken := parseImageSizeToken(size); isToken {
		if !registry.AllowsImageSize(m, token) {
			return GeminiImageConfig{}, false, fmt.Errorf("imageSize %q not allowed for this model; allowed: %s", token, strings.Join(m.ImageSizes, ", "))
		}
		ar := aspect
		if ar == "" {
			ar = defaultAspect(m)
		}
		return GeminiImageConfig{AspectRatio: ar, ImageSize: token}, true, nil
	}

	mapped, err := mapWxHToGemini(size)
	if err != nil {
		return GeminiImageConfig{}, false, err
	}
	if aspect != "" {
		mapped.AspectRatio = aspect
	}
	if !registry.AllowsImageSize(m, mapped.ImageSize) {
		return GeminiImageConfig{}, false, fmt.Errorf("mapped imageSize %q from %q not allowed for this model; allowed: %s", mapped.ImageSize, size, strings.Join(m.ImageSizes, ", "))
	}
	if err := registry.ValidateAspect(m, mapped.AspectRatio); err != nil {
		return GeminiImageConfig{}, false, err
	}
	return mapped, true, nil
}

func parseImageSizeToken(size string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(size)) {
	case "512":
		return "512", true
	case "1K":
		return "1K", true
	case "2K":
		return "2K", true
	case "4K":
		return "4K", true
	default:
		return "", false
	}
}

func pickDefaultImageSize(m registry.Model) string {
	if registry.AllowsImageSize(m, "1K") {
		return "1K"
	}
	if len(m.ImageSizes) > 0 {
		return m.ImageSizes[0]
	}
	return "1K"
}

func defaultAspect(m registry.Model) string {
	if err := registry.ValidateAspect(m, "1:1"); err == nil {
		return "1:1"
	}
	if len(m.AspectRatios) > 0 {
		return m.AspectRatios[0]
	}
	return "1:1"
}

func mapWxHToGemini(size string) (GeminiImageConfig, error) {
	switch strings.ToLower(size) {
	case "1024x1024":
		return GeminiImageConfig{AspectRatio: "1:1", ImageSize: "1K"}, nil
	case "2048x2048":
		return GeminiImageConfig{AspectRatio: "1:1", ImageSize: "2K"}, nil
	case "2048x1152":
		return GeminiImageConfig{AspectRatio: "16:9", ImageSize: "2K"}, nil
	case "3840x2160":
		return GeminiImageConfig{AspectRatio: "16:9", ImageSize: "4K"}, nil
	case "1536x1024":
		return GeminiImageConfig{AspectRatio: "3:2", ImageSize: "1K"}, nil
	case "1024x1536":
		return GeminiImageConfig{AspectRatio: "2:3", ImageSize: "1K"}, nil
	case "2160x3840":
		return GeminiImageConfig{AspectRatio: "9:16", ImageSize: "4K"}, nil
	default:
		return GeminiImageConfig{}, fmt.Errorf("cannot map size %q to Gemini imageConfig; use a known WxH, or 1K/2K/4K/512 with optional --aspect", size)
	}
}
