package provider

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func veoModel() registry.Model {
	return registry.Model{
		Provider: "gemini",
		Kind:     registry.KindVideo,
		APIID:    "veo-3.1-fast-generate-preview",
		Ops:      []string{"video"},
		PricePerSec: map[string]float64{
			"720p": 0.10,
		},
	}
}

func decodeVeoBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestBuildVeoRequestTextToVideo(t *testing.T) {
	b, err := BuildVeoRequest(VideoRequest{
		Model:      veoModel(),
		Prompt:     "a fox running through snow",
		Resolution: "720p",
		Aspect:     "16:9",
		Duration:   "8",
	})
	require.NoError(t, err)

	body := decodeVeoBody(t, b)
	instances := body["instances"].([]any)
	require.Len(t, instances, 1)
	inst := instances[0].(map[string]any)
	assert.Equal(t, "a fox running through snow", inst["prompt"])
	assert.NotContains(t, inst, "image")
	assert.NotContains(t, inst, "lastFrame")
	assert.NotContains(t, inst, "referenceImages")

	params := body["parameters"].(map[string]any)
	assert.Equal(t, "720p", params["resolution"])
	assert.Equal(t, "16:9", params["aspectRatio"])
	// The published REST docs show a string here; the live API rejects that
	// with "needs to be a number" — verified 2026-08-02.
	assert.Equal(t, float64(8), params["durationSeconds"])
	assert.NotContains(t, params, "negativePrompt")
	assert.NotContains(t, params, "personGeneration")
}

func TestBuildVeoRequestRejectsNonNumericDuration(t *testing.T) {
	_, err := BuildVeoRequest(VideoRequest{Model: veoModel(), Prompt: "x", Duration: "eight"})
	assert.ErrorContains(t, err, "not an integer number of seconds")
}

func TestBuildVeoRequestImageToVideo(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	b, err := BuildVeoRequest(VideoRequest{
		Model:            veoModel(),
		Prompt:           "pan across the scene",
		Image:            png,
		ImageName:        "first.png",
		NegativePrompt:   "blurry",
		PersonGeneration: "allow_adult",
	})
	require.NoError(t, err)

	body := decodeVeoBody(t, b)
	inst := body["instances"].([]any)[0].(map[string]any)
	// Flat predict-style encoding: the live API rejects {"inlineData":{…}}.
	img := inst["image"].(map[string]any)
	assert.Equal(t, "image/png", img["mimeType"])
	assert.Equal(t, base64.StdEncoding.EncodeToString(png), img["bytesBase64Encoded"])
	assert.NotContains(t, img, "inlineData")

	params := body["parameters"].(map[string]any)
	assert.Equal(t, "blurry", params["negativePrompt"])
	assert.Equal(t, "allow_adult", params["personGeneration"])
	// Unset knobs are omitted so the API applies its own defaults.
	assert.NotContains(t, params, "resolution")
	assert.NotContains(t, params, "durationSeconds")
}

func TestBuildVeoRequestLastFrameAndRefs(t *testing.T) {
	first := []byte{1}
	last := []byte{2}
	ref := []byte{3}

	b, err := BuildVeoRequest(VideoRequest{
		Model:         veoModel(),
		Prompt:        "swing in the fog",
		Image:         first,
		ImageName:     "a.png",
		LastFrame:     last,
		LastFrameName: "b.jpg",
		RefImages:     [][]byte{ref},
		RefImageNames: []string{"dress.webp"},
	})
	require.NoError(t, err)

	inst := decodeVeoBody(t, b)["instances"].([]any)[0].(map[string]any)
	lastData := inst["lastFrame"].(map[string]any)
	assert.Equal(t, "image/jpeg", lastData["mimeType"], "mime follows the file extension")

	refs := inst["referenceImages"].([]any)
	require.Len(t, refs, 1)
	r0 := refs[0].(map[string]any)
	assert.Equal(t, "asset", r0["referenceType"])
	refData := r0["image"].(map[string]any)
	assert.Equal(t, "image/webp", refData["mimeType"])
	assert.Equal(t, base64.StdEncoding.EncodeToString(ref), refData["bytesBase64Encoded"])
}

func TestBuildVeoRequestErrors(t *testing.T) {
	_, err := BuildVeoRequest(VideoRequest{Model: veoModel()})
	assert.ErrorContains(t, err, "prompt or --image required")

	_, err = BuildVeoRequest(VideoRequest{Model: veoModel(), Prompt: "x", LastFrame: []byte{1}})
	assert.ErrorContains(t, err, "--last-frame requires --image")
}

func TestParseVeoStart(t *testing.T) {
	name, err := ParseVeoStart([]byte(`{"name":"models/veo-3.1-fast-generate-preview/operations/abc123"}`))
	require.NoError(t, err)
	assert.Equal(t, "models/veo-3.1-fast-generate-preview/operations/abc123", name)

	_, err = ParseVeoStart([]byte(`{"error":{"message":"quota exceeded"}}`))
	assert.ErrorContains(t, err, "quota exceeded")

	_, err = ParseVeoStart([]byte(`{}`))
	assert.ErrorContains(t, err, "no operation name")

	_, err = ParseVeoStart([]byte(`not json`))
	assert.Error(t, err)
}

func TestParseVeoOperationPending(t *testing.T) {
	op, err := ParseVeoOperation([]byte(`{"name":"models/m/operations/x"}`))
	require.NoError(t, err)
	assert.False(t, op.Done)
	assert.Empty(t, op.URIs)
	assert.Empty(t, op.Error)
}

func TestParseVeoOperationDoneGeneratedSamples(t *testing.T) {
	op, err := ParseVeoOperation([]byte(`{
      "name":"models/m/operations/x",
      "done":true,
      "response":{"generateVideoResponse":{"generatedSamples":[
        {"video":{"uri":"https://example.test/v.mp4"}}]}}}`))
	require.NoError(t, err)
	assert.True(t, op.Done)
	assert.Equal(t, []string{"https://example.test/v.mp4"}, op.URIs)
	assert.Empty(t, op.Error)
}

func TestParseVeoOperationDoneAlternateShape(t *testing.T) {
	op, err := ParseVeoOperation([]byte(`{
      "done":true,
      "response":{"generatedVideos":[{"video":{"uri":"https://example.test/alt.mp4"}}]}}`))
	require.NoError(t, err)
	assert.True(t, op.Done)
	assert.Equal(t, []string{"https://example.test/alt.mp4"}, op.URIs)
}

func TestParseVeoOperationErrors(t *testing.T) {
	op, err := ParseVeoOperation([]byte(`{"error":{"code":400,"message":"bad prompt"}}`))
	require.NoError(t, err)
	assert.True(t, op.Done, "an errored operation is terminal")
	assert.Equal(t, "bad prompt", op.Error)

	op, err = ParseVeoOperation([]byte(`{
      "done":true,
      "response":{"generateVideoResponse":{"generatedSamples":[],
        "raiMediaFilteredCount":1,"raiMediaFilteredReasons":["safety"]}}}`))
	require.NoError(t, err)
	assert.Contains(t, op.Error, "safety")

	op, err = ParseVeoOperation([]byte(`{"done":true,"response":{}}`))
	require.NoError(t, err)
	assert.Contains(t, op.Error, "without a video URI")
}

func TestOperationURL(t *testing.T) {
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models/m/operations/x",
		OperationURL("models/m/operations/x"))
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models/m/operations/x",
		OperationURL("/models/m/operations/x"))
	assert.Equal(t, "https://other.test/op", OperationURL("https://other.test/op"))
}

func TestDownloadURL(t *testing.T) {
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/files/abc:download?alt=media",
		DownloadURL("https://generativelanguage.googleapis.com/v1beta/files/abc:download"))
	assert.Equal(t,
		"https://x.test/files/abc:download?alt=media",
		DownloadURL("https://x.test/files/abc:download?alt=media"))
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/files/abc:download?alt=media",
		DownloadURL("files/abc:download"))
	assert.Equal(t, "https://x.test/plain.mp4", DownloadURL("https://x.test/plain.mp4"))
	assert.Equal(t, "", DownloadURL(""))
}

func TestVideoExtForMime(t *testing.T) {
	assert.Equal(t, "mp4", VideoExtForMime("video/mp4"))
	assert.Equal(t, "mp4", VideoExtForMime(""))
	assert.Equal(t, "webm", VideoExtForMime("video/webm"))
	assert.Equal(t, "mov", VideoExtForMime("video/quicktime"))
	assert.Equal(t, "mp4", VideoExtForMime("application/octet-stream"))
}

func TestVeoImplementsVideoProvider(t *testing.T) {
	var _ VideoProvider = NewVeo("key", nil)
}
