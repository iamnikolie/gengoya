package provider

import (
	"fmt"
	"strings"
)

// ValidateOpenAIExtras checks background/compression/moderation against format and model.
func ValidateOpenAIExtras(r GenRequest) error {
	bg := strings.ToLower(strings.TrimSpace(r.Background))
	switch bg {
	case "", "auto", "transparent", "opaque":
	default:
		return fmt.Errorf("--background must be auto|transparent|opaque")
	}
	if bg == "transparent" && strings.EqualFold(r.Model.APIID, "gpt-image-2") {
		return fmt.Errorf("--background transparent is not supported for gpt-image-2")
	}
	if bg == "transparent" && r.Format != "" && r.Format != "png" && r.Format != "webp" {
		return fmt.Errorf("--background transparent requires --format png or webp")
	}

	mod := strings.ToLower(strings.TrimSpace(r.Moderation))
	switch mod {
	case "", "auto", "low":
	default:
		return fmt.Errorf("--moderation must be auto|low")
	}

	if r.Compression >= 0 {
		if r.Compression > 100 {
			return fmt.Errorf("--compression must be 0..100")
		}
		f := strings.ToLower(r.Format)
		if f != "jpeg" && f != "webp" {
			return fmt.Errorf("--compression requires --format jpeg or webp")
		}
	}
	return nil
}

// OpenAIExtrasForGeminiError is returned when OpenAI-only flags are set on Gemini.
func OpenAIExtrasForGeminiError(background string, compression int, moderation string) error {
	if background != "" || moderation != "" || compression >= 0 {
		return fmt.Errorf("--background/--compression/--moderation are OpenAI-only")
	}
	return nil
}
