package cmd

import (
	"fmt"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/registry"
)

// resolvedSelection is the model/provider chosen for a call.
type resolvedSelection struct {
	Alias    string
	Model    registry.Model
	Provider string
}

// resolveModelProvider applies --model / --provider / profile defaults.
//
// Rules (SPEC §3):
//   - if --model given, resolve its provider from the registry; if --provider
//     also given and disagrees, error
//   - if only --provider, use that provider's registry default model
//   - if neither, use profile default_provider + default_model (falling back to
//     the provider's registry default when default_model is empty)
func resolveModelProvider(reg *registry.Registry, cfg *config.Config, modelFlag, providerFlag string) (resolvedSelection, error) {
	if modelFlag != "" {
		m, ok := reg.Lookup(modelFlag)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("unknown model alias %q — run 'gengoya models --config <name>'", modelFlag)
		}
		if providerFlag != "" && providerFlag != m.Provider {
			return resolvedSelection{}, fmt.Errorf("--model %q belongs to provider %q, but --provider %q was set", modelFlag, m.Provider, providerFlag)
		}
		return resolvedSelection{Alias: modelFlag, Model: m, Provider: m.Provider}, nil
	}

	if providerFlag != "" {
		alias, ok := reg.DefaultFor(providerFlag)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("no default model for provider %q", providerFlag)
		}
		m, ok := reg.Lookup(alias)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("default model %q for provider %q missing from registry", alias, providerFlag)
		}
		return resolvedSelection{Alias: alias, Model: m, Provider: providerFlag}, nil
	}

	provider := cfg.DefaultProvider
	if provider == "" {
		return resolvedSelection{}, fmt.Errorf("no provider: set --provider or default_provider in config")
	}
	alias := cfg.DefaultModel
	if alias == "" {
		var ok bool
		alias, ok = reg.DefaultFor(provider)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("no default model for provider %q", provider)
		}
	}
	m, ok := reg.Lookup(alias)
	if !ok {
		return resolvedSelection{}, fmt.Errorf("unknown model alias %q — run 'gengoya models --config <name>'", alias)
	}
	if m.Provider != provider {
		return resolvedSelection{}, fmt.Errorf("default_model %q belongs to provider %q, but default_provider is %q", alias, m.Provider, provider)
	}
	return resolvedSelection{Alias: alias, Model: m, Provider: provider}, nil
}

// resolveVideoModelProvider is resolveModelProvider for `gengoya video`: it
// selects among kind: video models and falls back to the registry's
// video_defaults rather than the image defaults.
func resolveVideoModelProvider(reg *registry.Registry, cfg *config.Config, modelFlag, providerFlag string) (resolvedSelection, error) {
	if modelFlag != "" {
		m, ok := reg.Lookup(modelFlag)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("unknown model alias %q — run 'gengoya models --config <name>'", modelFlag)
		}
		if registry.IsMusic(m) || registry.IsSpeech(m) {
			return resolvedSelection{}, fmt.Errorf("model %q is a %s model, not video; run 'gengoya models --kind video' for video aliases", modelFlag, m.Kind)
		}
		if !registry.IsVideo(m) {
			return resolvedSelection{}, fmt.Errorf("model %q is an image model; run 'gengoya models --kind video' for video aliases", modelFlag)
		}
		if providerFlag != "" && providerFlag != m.Provider {
			return resolvedSelection{}, fmt.Errorf("--model %q belongs to provider %q, but --provider %q was set", modelFlag, m.Provider, providerFlag)
		}
		return resolvedSelection{Alias: modelFlag, Model: m, Provider: m.Provider}, nil
	}

	// Candidate providers, most specific first: --provider, profile default,
	// then the sole provider that declares a video default.
	var candidates []string
	if providerFlag != "" {
		candidates = []string{providerFlag}
	} else {
		if cfg.DefaultProvider != "" {
			candidates = append(candidates, cfg.DefaultProvider)
		}
		candidates = append(candidates, reg.VideoProviders()...)
	}

	for _, p := range candidates {
		alias, ok := reg.DefaultVideoFor(p)
		if !ok {
			continue
		}
		m, ok := reg.Lookup(alias)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("default video model %q for provider %q missing from registry", alias, p)
		}
		if !registry.IsVideo(m) {
			return resolvedSelection{}, fmt.Errorf("video_defaults.%s points at %q, which is not a video model", p, alias)
		}
		return resolvedSelection{Alias: alias, Model: m, Provider: p}, nil
	}

	if providerFlag != "" {
		return resolvedSelection{}, fmt.Errorf("provider %q has no video model in the registry", providerFlag)
	}
	return resolvedSelection{}, fmt.Errorf("no video model in the registry — check video_defaults")
}

// applySizeQualityAspect fills size/aspect/quality from flags or model defaults.
func applySizeQualityAspect(m registry.Model, sizeFlag, aspectFlag, qualityFlag string) (size, aspect, quality string, err error) {
	size = sizeFlag
	if size == "" {
		size = registry.DefaultSize(m)
	}
	if err := registry.ValidateSize(m, size); err != nil {
		return "", "", "", err
	}

	aspect = aspectFlag
	if err := registry.ValidateAspect(m, aspect); err != nil {
		return "", "", "", err
	}

	// Early Gemini mapping check so lite+4K etc. fail before the HTTP call.
	if m.Provider == "gemini" {
		if _, _, mapErr := mapGeminiCheck(m, size, aspect); mapErr != nil {
			return "", "", "", mapErr
		}
	}

	quality = qualityFlag
	if quality == "" {
		quality = registry.DefaultQuality(m)
	}
	if err := registry.ValidateQuality(m, quality); err != nil {
		return "", "", "", err
	}
	return size, aspect, quality, nil
}
