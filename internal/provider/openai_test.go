package provider

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildOpenAIGenerateBody(t *testing.T) {
	r := GenRequest{
		Model:       registry.Model{APIID: "gpt-image-2", PricePerImg: 0.05},
		Prompt:      "a red cube",
		N:           2,
		Size:        "1024x1024",
		Quality:     "high",
		Format:      "png",
		Compression: -1,
	}
	b, err := BuildOpenAIGenerateBody(r)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "gpt-image-2", m["model"])
	assert.Equal(t, "a red cube", m["prompt"])
	assert.Equal(t, float64(2), m["n"])
	assert.Equal(t, "1024x1024", m["size"])
	assert.Equal(t, "high", m["quality"])
	assert.Equal(t, "png", m["output_format"])
}

func TestBuildOpenAIGenerateBodyOmitsAuto(t *testing.T) {
	r := GenRequest{
		Model:       registry.Model{APIID: "gpt-image-2"},
		Prompt:      "x",
		N:           1,
		Size:        "auto",
		Compression: -1,
	}
	b, err := BuildOpenAIGenerateBody(r)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	_, ok := m["size"]
	assert.False(t, ok)
	_, ok = m["quality"]
	assert.False(t, ok)
}

func TestParseOpenAIResponseUsageCost(t *testing.T) {
	payload := []byte("hello-img")
	b64 := base64.StdEncoding.EncodeToString(payload)
	raw := []byte(`{
		"created":1,
		"data":[{"b64_json":"` + b64 + `"}],
		"usage":{
			"input_tokens":24,
			"output_tokens":1000,
			"total_tokens":1024,
			"input_tokens_details":{"text_tokens":24,"image_tokens":0}
		}
	}`)
	r := GenRequest{
		Model: registry.Model{
			PricePerImg:   0.05,
			PriceTextIn:   5.0,
			PriceImageIn:  10.0,
			PriceImageOut: 40.0,
			Sizes:         []string{"auto", "1024x1024"},
		},
		Size:        "1024x1024",
		N:           1,
		Format:      "png",
		Compression: -1,
	}
	out, err := ParseOpenAIResponse(raw, r)
	require.NoError(t, err)
	require.Len(t, out.Images, 1)
	assert.Equal(t, payload, out.Images[0].Data)
	assert.Equal(t, CostSourceUsage, out.CostSource)
	assert.InDelta(t, (24*5.0+1000*40.0)/1e6, out.CostUSD, 1e-9)
	require.NotNil(t, out.Usage)
}

func TestParseOpenAIResponseFallbackCost(t *testing.T) {
	payload := []byte("hello-img")
	b64 := base64.StdEncoding.EncodeToString(payload)
	raw := []byte(`{"created":1,"data":[{"b64_json":"` + b64 + `"}]}`)
	r := GenRequest{
		Model:       registry.Model{PricePerImg: 0.05},
		N:           1,
		Format:      "png",
		Compression: -1,
	}
	out, err := ParseOpenAIResponse(raw, r)
	require.NoError(t, err)
	assert.Equal(t, CostSourceRegistry, out.CostSource)
	assert.InDelta(t, 0.05, out.CostUSD, 1e-9)
}

func TestBuildOpenAIEditMultipart(t *testing.T) {
	r := GenRequest{
		Model:       registry.Model{APIID: "gpt-image-2"},
		Prompt:      "make blue",
		N:           1,
		Format:      "png",
		Images:      [][]byte{[]byte("PNGDATA")},
		ImageNames:  []string{"cat.png"},
		Mask:        []byte("MASK"),
		Compression: -1,
	}
	body, ct, err := BuildOpenAIEditMultipart(r)
	require.NoError(t, err)
	assert.Contains(t, ct, "multipart/form-data")
	assert.Contains(t, string(body), "image[]")
	assert.Contains(t, string(body), "cat.png")
	assert.Contains(t, string(body), "PNGDATA")
	assert.Contains(t, string(body), "mask")
	assert.Contains(t, string(body), "make blue")
}
