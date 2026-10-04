package cmd

import (
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
)

func validateProviderExtras(providerName string, m registry.Model, background string, compression int, moderation, format string) error {
	if providerName == "gemini" {
		return provider.OpenAIExtrasForGeminiError(background, compression, moderation)
	}
	return provider.ValidateOpenAIExtras(provider.GenRequest{
		Model:       m,
		Background:  background,
		Compression: compression,
		Moderation:  moderation,
		Format:      format,
	})
}

func openaiExtrasNote(background, format string) string {
	if background == "transparent" && format == "jpeg" {
		return "note: --background transparent with jpeg may be rejected; prefer png or webp"
	}
	return ""
}

func fillOpenAIExtras(req *provider.GenRequest) {
	req.Background = backgroundFlag
	req.Compression = compressionFlag
	req.Moderation = moderationFlag
}
