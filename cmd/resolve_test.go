package cmd

import (
	"testing"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testReg(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Load("")
	require.NoError(t, err)
	return r
}

func TestResolveModelFlag(t *testing.T) {
	reg := testReg(t)
	cfg := &config.Config{DefaultProvider: "openai"}

	sel, err := resolveModelProvider(reg, cfg, "nano-banana-2", "")
	require.NoError(t, err)
	assert.Equal(t, "gemini", sel.Provider)
	assert.Equal(t, "nano-banana-2", sel.Alias)

	_, err = resolveModelProvider(reg, cfg, "nano-banana-2", "openai")
	require.Error(t, err)

	_, err = resolveModelProvider(reg, cfg, "no-such", "")
	require.Error(t, err)
}

func TestResolveProviderOnly(t *testing.T) {
	reg := testReg(t)
	cfg := &config.Config{DefaultProvider: "openai", DefaultModel: "gpt-image-1"}

	sel, err := resolveModelProvider(reg, cfg, "", "gemini")
	require.NoError(t, err)
	assert.Equal(t, "gemini", sel.Provider)
	assert.Equal(t, "nano-banana-2", sel.Alias) // registry default, not profile model
}

func TestResolveNeitherUsesProfile(t *testing.T) {
	reg := testReg(t)
	cfg := &config.Config{DefaultProvider: "openai", DefaultModel: "gpt-image-1-mini"}

	sel, err := resolveModelProvider(reg, cfg, "", "")
	require.NoError(t, err)
	assert.Equal(t, "openai", sel.Provider)
	assert.Equal(t, "gpt-image-1-mini", sel.Alias)
}

func TestResolveNeitherFallsBackToRegistryDefault(t *testing.T) {
	reg := testReg(t)
	cfg := &config.Config{DefaultProvider: "gemini"}

	sel, err := resolveModelProvider(reg, cfg, "", "")
	require.NoError(t, err)
	assert.Equal(t, "nano-banana-2", sel.Alias)
}

func TestApplySizeQualityAspect(t *testing.T) {
	m := registry.Model{
		Provider:  "openai",
		SizeMode:  registry.SizeModeEnum,
		Sizes:     []string{"auto", "1024x1024"},
		Qualities: []string{"auto", "low", "high"},
	}
	size, aspect, q, err := applySizeQualityAspect(m, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, "auto", size)
	assert.Empty(t, aspect)
	assert.Equal(t, "auto", q)

	_, _, _, err = applySizeQualityAspect(m, "512x512", "", "")
	require.Error(t, err)

	_, _, _, err = applySizeQualityAspect(m, "1024x1024", "16:9", "")
	require.Error(t, err)
}
