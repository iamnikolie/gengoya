package cmd

import (
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
)

// mapGeminiCheck validates that size/aspect map cleanly for the model.
func mapGeminiCheck(m registry.Model, size, aspect string) (provider.GeminiImageConfig, bool, error) {
	return provider.MapGeminiParams(m, size, aspect)
}
