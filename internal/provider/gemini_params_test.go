package provider

import (
	"testing"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func geminiModel(t *testing.T, alias string) registry.Model {
	t.Helper()
	r, err := registry.Load("")
	require.NoError(t, err)
	m, ok := r.Lookup(alias)
	require.True(t, ok)
	return m
}

func TestMapGeminiParamsWxH(t *testing.T) {
	m := geminiModel(t, "nano-banana-2")
	cfg, ok, err := MapGeminiParams(m, "1024x1024", "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "1:1", cfg.AspectRatio)
	assert.Equal(t, "1K", cfg.ImageSize)

	cfg, ok, err = MapGeminiParams(m, "3840x2160", "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "16:9", cfg.AspectRatio)
	assert.Equal(t, "4K", cfg.ImageSize)
}

func TestMapGeminiParamsAutoOmits(t *testing.T) {
	m := geminiModel(t, "nano-banana-2")
	_, ok, err := MapGeminiParams(m, "auto", "")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestMapGeminiParamsAspectAlone(t *testing.T) {
	m := geminiModel(t, "nano-banana-2")
	cfg, ok, err := MapGeminiParams(m, "auto", "9:16")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "9:16", cfg.AspectRatio)
	assert.Equal(t, "1K", cfg.ImageSize)
}

func TestMapGeminiParamsNativeToken(t *testing.T) {
	m := geminiModel(t, "nano-banana-2")
	cfg, ok, err := MapGeminiParams(m, "2K", "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "1:1", cfg.AspectRatio)
	assert.Equal(t, "2K", cfg.ImageSize)
}

func TestMapGeminiParamsLiteRejects2K(t *testing.T) {
	m := geminiModel(t, "nano-banana-2-lite")
	_, _, err := MapGeminiParams(m, "2K", "")
	require.Error(t, err)

	_, _, err = MapGeminiParams(m, "2048x2048", "")
	require.Error(t, err)
}

func TestBuildGeminiRequestIncludesImageConfig(t *testing.T) {
	m := geminiModel(t, "nano-banana-2")
	b, err := BuildGeminiRequest(GenRequest{
		Model:  m,
		Prompt: "cube",
		Size:   "1024x1024",
	})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"imageConfig"`)
	assert.Contains(t, string(b), `"aspectRatio":"1:1"`)
	assert.Contains(t, string(b), `"imageSize":"1K"`)
}
