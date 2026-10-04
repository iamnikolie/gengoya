package provider

import (
	"testing"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCostFromUsage(t *testing.T) {
	m := registry.Model{
		PriceTextIn:   5.0,
		PriceImageIn:  10.0,
		PriceImageOut: 40.0,
	}
	u := &Usage{
		InputTokens:  24,
		OutputTokens: 1568,
		InputTokensDetails: &struct {
			TextTokens  int `json:"text_tokens"`
			ImageTokens int `json:"image_tokens"`
		}{TextTokens: 24, ImageTokens: 0},
	}
	usd, ok := CostFromUsage(m, u)
	require.True(t, ok)
	// (24*5 + 0*10 + 1568*40) / 1e6 = 0.06284
	assert.InDelta(t, 0.06284, usd, 1e-9)
}

func TestCostFromUsageFallback(t *testing.T) {
	_, ok := CostFromUsage(registry.Model{PricePerImg: 0.05}, nil)
	assert.False(t, ok)
	_, ok = CostFromUsage(registry.Model{}, &Usage{OutputTokens: 100})
	assert.False(t, ok)
}

func TestValidateOpenAIExtras(t *testing.T) {
	m2 := registry.Model{APIID: "gpt-image-2"}
	err := ValidateOpenAIExtras(GenRequest{Model: m2, Background: "transparent", Format: "png", Compression: -1})
	require.Error(t, err)

	m15 := registry.Model{APIID: "gpt-image-1.5"}
	require.NoError(t, ValidateOpenAIExtras(GenRequest{Model: m15, Background: "transparent", Format: "png", Compression: -1}))

	require.Error(t, ValidateOpenAIExtras(GenRequest{Model: m15, Compression: 50, Format: "png"}))
	require.NoError(t, ValidateOpenAIExtras(GenRequest{Model: m15, Compression: 50, Format: "jpeg"}))
	require.Error(t, ValidateOpenAIExtras(GenRequest{Model: m15, Moderation: "strict", Compression: -1}))
}

func TestBuildOpenAIBodyExtras(t *testing.T) {
	m := registry.Model{APIID: "gpt-image-1.5"}
	comp := 40
	b, err := BuildOpenAIGenerateBody(GenRequest{
		Model:       m,
		Prompt:      "x",
		N:           1,
		Format:      "webp",
		Background:  "opaque",
		Compression: comp,
		Moderation:  "low",
	})
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"background":"opaque"`)
	assert.Contains(t, s, `"output_compression":40`)
	assert.Contains(t, s, `"moderation":"low"`)
}
