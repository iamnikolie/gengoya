package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEmbedded(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)

	m, ok := r.Lookup("gpt-image-2")
	require.True(t, ok)
	assert.Equal(t, "openai", m.Provider)
	assert.Equal(t, "gpt-image-2", m.APIID)
	assert.Contains(t, m.Ops, "generate")
	assert.Contains(t, m.Ops, "edit")

	alias, ok := r.DefaultFor("openai")
	require.True(t, ok)
	assert.Equal(t, "gpt-image-2.5-flare", alias)

	alias, ok = r.DefaultFor("gemini")
	require.True(t, ok)
	assert.Equal(t, "nano-banana-2", alias)

	gm, ok := r.Lookup("nano-banana-2")
	require.True(t, ok)
	assert.Equal(t, "gemini", gm.Provider)
	assert.Equal(t, "gemini-3.1-flash-image", gm.APIID)
	assert.Empty(t, gm.Qualities)

	all := r.All()
	assert.GreaterOrEqual(t, len(all), 8)
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	override := `
defaults:
  openai: custom-model
models:
  custom-model:
    provider: openai
    api_id: custom-api
    ops: [generate]
    sizes: [1024x1024]
    qualities: [high]
    price_note: "test"
    price_per_img: 0.01
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte(override), 0600))

	r, err := Load(dir)
	require.NoError(t, err)
	_, ok := r.Lookup("gpt-image-2")
	assert.False(t, ok)
	m, ok := r.Lookup("custom-model")
	require.True(t, ok)
	assert.Equal(t, "custom-api", m.APIID)
	alias, ok := r.DefaultFor("openai")
	require.True(t, ok)
	assert.Equal(t, "custom-model", alias)
}

func TestValidateSize(t *testing.T) {
	m := Model{SizeMode: SizeModeEnum, Sizes: []string{"auto", "1024x1024", "2048x2048"}}
	assert.NoError(t, ValidateSize(m, ""))
	assert.NoError(t, ValidateSize(m, "auto"))
	assert.NoError(t, ValidateSize(m, "1024x1024"))
	assert.Error(t, ValidateSize(m, "512x512"))
}

func TestValidateSizeFlexible(t *testing.T) {
	m := Model{SizeMode: SizeModeFlexible, Sizes: []string{"auto", "1024x1024"}}
	assert.NoError(t, ValidateSize(m, "1536x864"))
	assert.Error(t, ValidateSize(m, "10x10"))
	assert.Error(t, ValidateSize(m, "100x17")) // not multiple of 16
}

func TestValidateAspect(t *testing.T) {
	m := Model{Provider: "gemini", AspectRatios: []string{"1:1", "16:9"}}
	assert.NoError(t, ValidateAspect(m, ""))
	assert.NoError(t, ValidateAspect(m, "16:9"))
	assert.Error(t, ValidateAspect(m, "7:1"))
	assert.Error(t, ValidateAspect(Model{Provider: "openai"}, "1:1"))
}

func TestLoadGeminiCaps(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)
	m, ok := r.Lookup("nano-banana-2-lite")
	require.True(t, ok)
	assert.Equal(t, SizeModeGemini, m.SizeMode)
	assert.Equal(t, []string{"1K"}, m.ImageSizes)
	assert.Contains(t, m.AspectRatios, "16:9")
}

func TestValidateQuality(t *testing.T) {
	m := Model{Qualities: []string{"auto", "low", "high"}}
	assert.NoError(t, ValidateQuality(m, ""))
	assert.NoError(t, ValidateQuality(m, "high"))
	assert.Error(t, ValidateQuality(m, "ultra"))

	ignored := Model{Qualities: nil}
	assert.NoError(t, ValidateQuality(ignored, "high"))
}

// TestGPTImage25Ladder pins the one thing that separates the 2.5 pair from every
// earlier GPT Image model: two extra quality steps above high. The live API's
// enum error still names only low/medium/high/auto, so the registry is the
// contract here, not the error string.
func TestGPTImage25Ladder(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)

	for _, alias := range []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
		m, ok := r.Lookup(alias)
		require.True(t, ok, alias)
		assert.Equal(t, "openai", m.Provider)
		assert.Equal(t, SizeModeFlexible, m.SizeMode, "%s takes arbitrary WxH", alias)
		assert.NoError(t, ValidateQuality(m, "xhigh"), alias)
		assert.NoError(t, ValidateQuality(m, "max"), alias)
		assert.Error(t, ValidateQuality(m, "ultra"), alias)
		assert.InDelta(t, 30.0, m.PriceImageOut, 1e-9, "%s bills image output at $30/1M", alias)
	}

	// The ladder stops at high everywhere else, 2.x included.
	for _, alias := range []string{"gpt-image-2", "gpt-image-1.5", "gpt-image-1", "gpt-image-1-mini"} {
		m, ok := r.Lookup(alias)
		require.True(t, ok, alias)
		assert.NoError(t, ValidateQuality(m, "high"), alias)
		assert.Error(t, ValidateQuality(m, "xhigh"), alias)
		assert.Error(t, ValidateQuality(m, "max"), alias)
	}
}

func TestSupportsOp(t *testing.T) {
	m := Model{Ops: []string{"generate", "edit"}}
	assert.True(t, SupportsOp(m, "generate"))
	assert.False(t, SupportsOp(m, "upscale"))
}
