package provider

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGeminiRequestGenerate(t *testing.T) {
	r := GenRequest{
		Model:  registry.Model{APIID: "gemini-3.1-flash-image-preview"},
		Prompt: "a blue sphere",
		N:      1,
		Size:   "auto",
	}
	b, err := BuildGeminiRequest(r)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	cfg := m["generationConfig"].(map[string]any)
	mods := cfg["responseModalities"].([]any)
	assert.Equal(t, []any{"IMAGE", "TEXT"}, mods)
	_, hasImg := cfg["imageConfig"]
	assert.False(t, hasImg)

	contents := m["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 1)
	assert.Equal(t, "a blue sphere", parts[0].(map[string]any)["text"])
}

func TestBuildGeminiRequestEdit(t *testing.T) {
	img := []byte("IMG")
	r := GenRequest{
		Model:      registry.Model{APIID: "x", ImageSizes: []string{"1K"}, AspectRatios: []string{"1:1"}},
		Prompt:     "edit me",
		Images:     [][]byte{img},
		ImageNames: []string{"in.png"},
		Mask:       []byte("MASK"),
		Size:       "auto",
	}
	b, err := BuildGeminiRequest(r)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	parts := m["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 3)

	inline := parts[0].(map[string]any)["inline_data"].(map[string]any)
	assert.Equal(t, "image/png", inline["mime_type"])
	decoded, err := base64.StdEncoding.DecodeString(inline["data"].(string))
	require.NoError(t, err)
	assert.Equal(t, img, decoded)
	assert.Equal(t, "edit me", parts[2].(map[string]any)["text"])
}

func TestParseGeminiResponse(t *testing.T) {
	payload := []byte("gemini-png")
	b64 := base64.StdEncoding.EncodeToString(payload)
	raw := []byte(`{
		"candidates":[{
			"content":{"parts":[
				{"text":"rewritten prompt here"},
				{"inlineData":{"mimeType":"image/jpeg","data":"` + b64 + `"}}
			]}
		}]
	}`)
	r := GenRequest{
		Model: registry.Model{PricePerImg: 0.045, Sizes: []string{"auto", "1024x1024"}},
		Size:  "auto",
	}
	out, err := ParseGeminiResponse(raw, r)
	require.NoError(t, err)
	require.Len(t, out.Images, 1)
	assert.Equal(t, payload, out.Images[0].Data)
	assert.Equal(t, "image/jpeg", out.Images[0].MimeType)
	assert.Equal(t, "rewritten prompt here", out.RevisedPrompt)
	assert.InDelta(t, 0.045, out.CostUSD, 1e-9)
}

func TestGeminiNotes(t *testing.T) {
	assert.Empty(t, GeminiQualityNote(""))
	assert.Contains(t, GeminiQualityNote("high"), "ignored")
	assert.Empty(t, GeminiFormatNote("png"))
	assert.Contains(t, GeminiFormatNote("webp"), "not sent")
}

func TestExtForMime(t *testing.T) {
	assert.Equal(t, "jpeg", ExtForMime("image/jpeg"))
	assert.Equal(t, "png", ExtForMime("image/png"))
	assert.Equal(t, "webp", ExtForMime("image/webp"))
}
