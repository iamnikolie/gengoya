package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func omniModel() registry.Model {
	return registry.Model{
		Provider: "gemini",
		Kind:     registry.KindVideo,
		APIID:    "gemini-omni-1.1-flash",
		Ops:      []string{"video"},
	}
}

func decodeOmniBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestIsOmniModel(t *testing.T) {
	assert.True(t, IsOmniModel("gemini-omni-1.1-flash"))
	assert.False(t, IsOmniModel("veo-3.1-fast-generate-preview"))
}

func TestBuildOmniRequestTextToVideo(t *testing.T) {
	b, err := BuildOmniRequest(VideoRequest{
		Model: omniModel(), Prompt: "a marble on a track",
		Resolution: "360p", Aspect: "9:16", Duration: "3",
	})
	require.NoError(t, err)
	body := decodeOmniBody(t, b)
	assert.Equal(t, "gemini-omni-1.1-flash", body["model"])
	// Text-only input is a plain string.
	assert.Equal(t, "a marble on a track", body["input"])
	rf := body["response_format"].(map[string]any)
	assert.Equal(t, "video", rf["type"])
	assert.Equal(t, "360p", rf["resolution"])
	assert.Equal(t, "9:16", rf["aspect_ratio"])
	// Bare "3" / 3 are rejected live ("Invalid input at 'response_format'");
	// the API wants a proto Duration string. Verified 2026-10-04.
	assert.Equal(t, "3s", rf["duration"])
	assert.NotContains(t, rf, "delivery", "720p and below stay inline")
	assert.NotContains(t, body, "background", "default is the blocking call")
	assert.NotContains(t, body, "previous_interaction_id")
}

func TestBuildOmniRequestOmitsUnset(t *testing.T) {
	b, err := BuildOmniRequest(VideoRequest{Model: omniModel(), Prompt: "x"})
	require.NoError(t, err)
	rf := decodeOmniBody(t, b)["response_format"].(map[string]any)
	assert.Equal(t, map[string]any{"type": "video"}, rf)
}

func TestBuildOmniRequestDeliveryAndDetach(t *testing.T) {
	b, err := BuildOmniRequest(VideoRequest{Model: omniModel(), Prompt: "x", Resolution: "1080p"})
	require.NoError(t, err)
	body := decodeOmniBody(t, b)
	assert.Equal(t, "uri", body["response_format"].(map[string]any)["delivery"])

	_, err = BuildOmniRequest(VideoRequest{Model: omniModel(), Prompt: "x", Detach: true})
	assert.ErrorContains(t, err, "--detach is not supported")

	b, err = BuildOmniRequest(VideoRequest{Model: omniModel(), Prompt: "x", Resolution: "1080p", Delivery: "inline"})
	require.NoError(t, err)
	assert.Equal(t, "inline", decodeOmniBody(t, b)["response_format"].(map[string]any)["delivery"])

	_, err = BuildOmniRequest(VideoRequest{Model: omniModel(), Prompt: "x", Delivery: "gcs"})
	assert.ErrorContains(t, err, "inline or uri")
}

func TestBuildOmniRequestImageToVideo(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 1}
	jpg := []byte{0xff, 0xd8, 9}
	b, err := BuildOmniRequest(VideoRequest{
		Model: omniModel(), Prompt: "forest to snow",
		Image: png, ImageName: "a.png", LastFrame: jpg, LastFrameName: "b.jpg",
	})
	require.NoError(t, err)
	parts := decodeOmniBody(t, b)["input"].([]any)
	require.Len(t, parts, 3, "first frame, last frame, then the prompt")
	p0 := parts[0].(map[string]any)
	assert.Equal(t, "image", p0["type"])
	assert.Equal(t, "image/png", p0["mime_type"])
	assert.Equal(t, base64.StdEncoding.EncodeToString(png), p0["data"])
	assert.Equal(t, "image/jpeg", parts[1].(map[string]any)["mime_type"])
	last := parts[2].(map[string]any)
	assert.Equal(t, "text", last["type"])
	assert.Equal(t, "forest to snow", last["text"])
}

func TestBuildOmniRequestRefsAndEdit(t *testing.T) {
	b, err := BuildOmniRequest(VideoRequest{
		Model: omniModel(), Prompt: "cat plays with yarn",
		RefImages: [][]byte{{1}, {2}}, RefImageNames: []string{"cat.png", "yarn.jpg"},
	})
	require.NoError(t, err)
	assert.Len(t, decodeOmniBody(t, b)["input"].([]any), 3)

	b, err = BuildOmniRequest(VideoRequest{
		Model: omniModel(), Prompt: "make the violin invisible",
		PreviousInteraction: "interactions/v1_abc",
	})
	require.NoError(t, err)
	body := decodeOmniBody(t, b)
	assert.Equal(t, "v1_abc", body["previous_interaction_id"], "operation prefix is stripped")
	assert.Equal(t, "make the violin invisible", body["input"])

	b, err = BuildOmniRequest(VideoRequest{
		Model: omniModel(), Prompt: "ripple", EditVideo: []byte{0, 0, 0, 0x20}, EditVideoName: "clip.mp4",
	})
	require.NoError(t, err)
	vp := decodeOmniBody(t, b)["input"].([]any)[0].(map[string]any)
	assert.Equal(t, "video", vp["type"])
	assert.Equal(t, "video/mp4", vp["mime_type"])
}

func TestBuildOmniRequestErrors(t *testing.T) {
	cases := []struct {
		name string
		r    VideoRequest
		want string
	}{
		{"no prompt", VideoRequest{Model: omniModel(), Image: []byte{1}}, "prompt is required"},
		{"negative", VideoRequest{Model: omniModel(), Prompt: "x", NegativePrompt: "blur"}, "--negative is not supported"},
		{"person", VideoRequest{Model: omniModel(), Prompt: "x", PersonGeneration: "allow_all"}, "Veo only"},
		{"last frame alone", VideoRequest{Model: omniModel(), Prompt: "x", LastFrame: []byte{1}}, "--last-frame requires --image"},
		{"image+ref", VideoRequest{Model: omniModel(), Prompt: "x", Image: []byte{1}, RefImages: [][]byte{{2}}}, "cannot be combined"},
		{"two edit sources", VideoRequest{Model: omniModel(), Prompt: "x", EditVideo: []byte{1}, PreviousInteraction: "v1"}, "not both"},
	}
	for _, c := range cases {
		_, err := BuildOmniRequest(c.r)
		assert.ErrorContains(t, err, c.want, c.name)
	}
}

func omniResponse(t *testing.T, status string, content any, usage any) []byte {
	t.Helper()
	m := map[string]any{
		"id": "v1_abc", "status": status, "object": "interaction", "model": "gemini-omni-1.1-flash",
		"steps": []any{
			map[string]any{"type": "user_input", "content": []any{map[string]any{"type": "text", "text": "x"}}},
			map[string]any{"type": "thought", "content": []any{map[string]any{"type": "thought", "text": "hmm"}}},
			map[string]any{"type": "model_output", "content": content},
		},
	}
	if usage != nil {
		m["usage"] = usage
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func TestParseOmniInteractionInline(t *testing.T) {
	mp4 := []byte("\x00\x00\x00\x20ftypisom")
	data := omniResponse(t, "completed", []any{
		map[string]any{"type": "video", "mime_type": "video/mp4", "data": base64.StdEncoding.EncodeToString(mp4)},
	}, map[string]any{
		"total_input_tokens":  1079,
		"total_output_tokens": 6423,
		// Live 2026-10-04: thought tokens are reported separately and are not
		// part of total_output_tokens.
		"total_thought_tokens":      424,
		"output_tokens_by_modality": []any{map[string]any{"modality": "video", "tokens": 5793}},
	})
	op, err := ParseOmniInteraction(data)
	require.NoError(t, err)
	assert.True(t, op.Done)
	assert.Empty(t, op.Error)
	assert.Equal(t, "interactions/v1_abc", op.Name)
	require.Len(t, op.Videos, 1)
	assert.Equal(t, mp4, op.Videos[0].Data)
	assert.Equal(t, "video/mp4", op.Videos[0].MimeType)
	assert.Equal(t, CostSourceUsage, op.CostSource)
	want := 1079*1.5/1e6 + 5793*17.5/1e6 + (630+424)*9.0/1e6
	assert.InDelta(t, want, op.CostUSD, 1e-9)
}

func TestParseOmniInteractionURI(t *testing.T) {
	data := omniResponse(t, "completed", []any{
		map[string]any{"type": "video", "mime_type": "video/mp4",
			"uri": "https://generativelanguage.googleapis.com/v1beta/files/x9t:download?alt=media"},
	}, nil)
	op, err := ParseOmniInteraction(data)
	require.NoError(t, err)
	assert.True(t, op.Done)
	assert.Empty(t, op.Videos)
	assert.Equal(t, []string{"https://generativelanguage.googleapis.com/v1beta/files/x9t:download?alt=media"}, op.URIs)
	assert.Empty(t, op.CostSource, "no usage reported -> registry estimate")
}

func TestParseOmniInteractionStates(t *testing.T) {
	// in_progress: a background create response carries no steps.
	op, err := ParseOmniInteraction([]byte(`{"id":"v1_abc","status":"in_progress","object":"interaction"}`))
	require.NoError(t, err)
	assert.False(t, op.Done)
	assert.Empty(t, op.Error)

	// completed without a video: safety block / empty output.
	op, err = ParseOmniInteraction(omniResponse(t, "completed", []any{map[string]any{"type": "text", "text": "sorry"}}, nil))
	require.NoError(t, err)
	assert.True(t, op.Done)
	assert.Contains(t, op.Error, "without a video")

	op, err = ParseOmniInteraction([]byte(`{"id":"v1_abc","status":"failed"}`))
	require.NoError(t, err)
	assert.Contains(t, op.Error, "failed")

	_, err = ParseOmniInteraction([]byte(`{"error":{"code":"content_blocked","message":"Input blocked"}}`))
	assert.ErrorContains(t, err, "Input blocked")

	_, err = ParseOmniInteraction([]byte(`not json`))
	assert.Error(t, err)
}

func TestOmniCostFromUsage(t *testing.T) {
	_, ok := OmniCostFromUsage(nil)
	assert.False(t, ok)
	// 720p, 3s, measured live 2026-10-04: 17,376 video tokens.
	usd, ok := OmniCostFromUsage(&omniUsage{
		TotalInputTokens: 16, TotalOutputTokens: 17870, TotalThoughtTokens: 233,
		OutputByModality: []omniTokens{{Modality: "video", Tokens: 17376}},
	})
	require.True(t, ok)
	assert.InDelta(t, 0.31, usd, 0.01)
}

func TestOmniInteractionID(t *testing.T) {
	assert.Equal(t, "v1_abc", OmniInteractionID("interactions/v1_abc"))
	assert.Equal(t, "v1_abc", OmniInteractionID(" v1_abc "))
}

func TestGeminiVideoRouting(t *testing.T) {
	g := NewGeminiVideo("k", nil)
	assert.Same(t, g.Omni, g.forOperation("interactions/v1_abc"))
	assert.Same(t, g.Veo, g.forOperation("models/veo-3.1-generate-preview/operations/abc"))

	veo := registry.Model{APIID: "veo-3.1-fast-generate-preview"}
	_, err := g.Start(context.Background(), VideoRequest{Model: veo, Prompt: "x", PreviousInteraction: "v1"})
	assert.ErrorContains(t, err, "Omni models only")
}

// Start (blocking, background=false) parks the finished clip; Poll serves it
// from memory so no GET /interactions/{id} is needed.
func TestOmniStartPollWaitNoGet(t *testing.T) {
	mp4 := []byte("videobytes")
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			http.Error(w, "blocked", http.StatusBadRequest)
			return
		}
		assert.Equal(t, "k", r.Header.Get("x-goog-api-key"))
		w.Write(omniResponse(t, "completed", []any{
			map[string]any{"type": "video", "mime_type": "video/mp4", "data": base64.StdEncoding.EncodeToString(mp4)},
		}, nil))
	}))
	defer srv.Close()

	o := NewOmni("k", nil)
	o.Base = srv.URL
	name, err := o.Start(context.Background(), VideoRequest{Model: omniModel(), Prompt: "x"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(name, OperationPrefixOmni))

	op, err := o.Wait(context.Background(), name, time.Millisecond, nil)
	require.NoError(t, err)
	require.Len(t, op.Videos, 1)
	assert.Equal(t, mp4, op.Videos[0].Data)
	assert.Equal(t, 0, gets)
}
