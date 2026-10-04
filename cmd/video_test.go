package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/jobs"
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Load("")
	require.NoError(t, err)
	return r
}

func videoModelFor(t *testing.T, alias string) registry.Model {
	t.Helper()
	m, ok := testRegistry(t).Lookup(alias)
	require.True(t, ok)
	return m
}

func TestResolveVideoModelProviderDefaults(t *testing.T) {
	reg := testRegistry(t)

	// Profile default is an image provider with no video model: fall through to
	// the registry's video_defaults instead of erroring.
	sel, err := resolveVideoModelProvider(reg, &config.Config{DefaultProvider: "openai", DefaultModel: "gpt-image-2"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "veo-3.1-fast", sel.Alias)
	assert.Equal(t, "gemini", sel.Provider)
	assert.True(t, registry.IsVideo(sel.Model))

	// Gemini profile default resolves to the same video default.
	sel, err = resolveVideoModelProvider(reg, &config.Config{DefaultProvider: "gemini"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "veo-3.1-fast", sel.Alias)
}

func TestResolveVideoModelProviderExplicit(t *testing.T) {
	reg := testRegistry(t)
	cfg := &config.Config{DefaultProvider: "openai"}

	sel, err := resolveVideoModelProvider(reg, cfg, "veo-3.1", "")
	require.NoError(t, err)
	assert.Equal(t, "veo-3.1", sel.Alias)
	assert.Equal(t, "gemini", sel.Provider)

	sel, err = resolveVideoModelProvider(reg, cfg, "", "gemini")
	require.NoError(t, err)
	assert.Equal(t, "veo-3.1-fast", sel.Alias)
}

func TestResolveVideoModelProviderErrors(t *testing.T) {
	reg := testRegistry(t)
	cfg := &config.Config{DefaultProvider: "gemini"}

	_, err := resolveVideoModelProvider(reg, cfg, "nano-banana-2", "")
	assert.ErrorContains(t, err, "is an image model")

	_, err = resolveVideoModelProvider(reg, cfg, "no-such-model", "")
	assert.ErrorContains(t, err, "unknown model alias")

	_, err = resolveVideoModelProvider(reg, cfg, "veo-3.1", "openai")
	assert.ErrorContains(t, err, "belongs to provider")

	_, err = resolveVideoModelProvider(reg, cfg, "", "openai")
	assert.ErrorContains(t, err, "no video model")
}

func TestResolveVideoParamsDefaults(t *testing.T) {
	m := videoModelFor(t, "veo-3.1-fast")

	res, dur, asp, err := resolveVideoParams(m, "", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, "720p", res)
	assert.Equal(t, "8", dur)
	assert.Equal(t, "16:9", asp)
}

func TestResolveVideoParamsConstraints(t *testing.T) {
	m := videoModelFor(t, "veo-3.1")

	// 1080p implies 8s when the flag is omitted.
	res, dur, _, err := resolveVideoParams(m, "1080p", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, "1080p", res)
	assert.Equal(t, "8", dur)

	// …and rejects a conflicting explicit duration rather than silently fixing it.
	_, _, _, err = resolveVideoParams(m, "1080p", "4", "", 0)
	assert.ErrorContains(t, err, "requires --duration 8")

	// 720p keeps a short duration.
	_, dur, _, err = resolveVideoParams(m, "720p", "4", "", 0)
	require.NoError(t, err)
	assert.Equal(t, "4", dur)

	// Reference images force 8s.
	_, dur, _, err = resolveVideoParams(m, "720p", "", "", 2)
	require.NoError(t, err)
	assert.Equal(t, "8", dur)
	_, _, _, err = resolveVideoParams(m, "720p", "6", "", 2)
	assert.ErrorContains(t, err, "reference images require --duration 8")
	_, _, _, err = resolveVideoParams(m, "720p", "8", "", 4)
	assert.ErrorContains(t, err, "at most 3 reference image")
}

func TestResolveVideoParamsRejectsBadValues(t *testing.T) {
	m := videoModelFor(t, "veo-3.1-lite")

	_, _, _, err := resolveVideoParams(m, "4k", "", "", 0)
	assert.ErrorContains(t, err, "resolution \"4k\" not allowed")

	_, _, _, err = resolveVideoParams(m, "720p", "10", "", 0)
	assert.ErrorContains(t, err, "duration \"10\" not allowed")

	_, _, _, err = resolveVideoParams(m, "720p", "8", "1:1", 0)
	assert.ErrorContains(t, err, "aspect \"1:1\" not allowed")

	_, _, asp, err := resolveVideoParams(m, "720p", "8", "9:16", 0)
	require.NoError(t, err)
	assert.Equal(t, "9:16", asp)
}

func TestBuildVideoFilenames(t *testing.T) {
	mp4 := provider.Video{MimeType: "video/mp4"}

	names := buildVideoFilenames("a fox running through deep snow at dawn", "", []provider.Video{mp4})
	require.Len(t, names, 1)
	assert.True(t, strings.HasPrefix(names[0], "a-fox-running-through-deep-snow-"), names[0])
	assert.True(t, strings.HasSuffix(names[0], ".mp4"), names[0])

	named := buildVideoFilenames("prompt", "clip", []provider.Video{mp4})
	assert.Equal(t, []string{"clip.mp4"}, named)

	two := buildVideoFilenames("prompt", "clip", []provider.Video{mp4, mp4})
	assert.Equal(t, []string{"clip-1.mp4", "clip-2.mp4"}, two)

	webm := buildVideoFilenames("prompt", "clip", []provider.Video{{MimeType: "video/webm"}})
	assert.Equal(t, []string{"clip.webm"}, webm)

	assert.Nil(t, buildVideoFilenames("prompt", "clip", nil))
}

func TestPosterPathFor(t *testing.T) {
	assert.Equal(t, "/tmp/out/clip-poster.png", posterPathFor("/tmp/out/clip.mp4"))
	assert.Equal(t, "/tmp/out/a.b-poster.png", posterPathFor("/tmp/out/a.b.webm"))
}

func TestPosterGrid(t *testing.T) {
	c, r := posterGrid(1)
	assert.Equal(t, [2]int{1, 1}, [2]int{c, r})
	c, r = posterGrid(4)
	assert.Equal(t, [2]int{4, 1}, [2]int{c, r})
	c, r = posterGrid(6)
	assert.Equal(t, [2]int{4, 2}, [2]int{c, r})
	c, r = posterGrid(8)
	assert.Equal(t, [2]int{4, 2}, [2]int{c, r})
}

func TestPosterTimestamps(t *testing.T) {
	ts := posterTimestamps(8, 4)
	require.Len(t, ts, 4)
	assert.InDelta(t, 1.0, ts[0], 1e-9)
	assert.InDelta(t, 7.0, ts[3], 1e-9)
	for _, v := range ts {
		assert.Greater(t, v, 0.0)
		assert.Less(t, v, 8.0, "never sample at or past the final frame")
	}

	// Unknown duration falls back to a sane clip length instead of 0.
	ts = posterTimestamps(0, 2)
	require.Len(t, ts, 2)
	assert.Greater(t, ts[0], 0.0)

	assert.Nil(t, posterTimestamps(8, 0))
}

func TestEmitVideoResultPaths(t *testing.T) {
	arts := []videoArtifact{{Path: "/out/clip.mp4", Poster: "/out/clip-poster.png", Bytes: 42, Mime: "video/mp4"}}
	meta := jsonVideoResult{
		Model: "veo-3.1-fast", APIID: "veo-3.1-fast-generate-preview", Provider: "gemini",
		Resolution: "720p", DurationSeconds: "8", Aspect: "16:9",
		CostEstimateUSD: 0.80, ElapsedMS: 1234,
	}

	var out, errW bytes.Buffer
	require.NoError(t, emitVideoResult(arts, meta, false, &out, &errW))
	// Video path first, poster second — the poster is the Read-able artifact.
	assert.Equal(t, "/out/clip.mp4\n/out/clip-poster.png\n", out.String())
	assert.Contains(t, errW.String(), "model=veo-3.1-fast")
	assert.Contains(t, errW.String(), "res=720p dur=8s aspect=16:9")
	assert.Contains(t, errW.String(), "cost≈$0.80")

	out.Reset()
	errW.Reset()
	require.NoError(t, emitVideoResult(arts, meta, true, &out, &errW))
	var got jsonVideoResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got.Videos, 1)
	assert.Equal(t, "/out/clip.mp4", got.Videos[0].Path)
	assert.Equal(t, "/out/clip-poster.png", got.Videos[0].Poster)
	assert.Equal(t, 42, got.Videos[0].Bytes)
	assert.InDelta(t, 0.80, got.CostEstimateUSD, 1e-9)
	assert.Equal(t, int64(1234), got.ElapsedMS)
}

// The --detach JSON shape is what an agent parses to get the operation back;
// skill.md documents "videos": [], so it must not become null.
func TestEmitVideoResultDetachShape(t *testing.T) {
	meta := jsonVideoResult{
		Model: "veo-3.1-fast", APIID: "veo-3.1-fast-generate-preview", Provider: "gemini",
		Resolution: "720p", DurationSeconds: "8", Aspect: "16:9",
		Operation: "models/m/operations/abc", JobID: "abc", State: "pending",
		CostEstimateUSD: 0.80, CostSource: "registry",
	}
	var out, errW bytes.Buffer
	require.NoError(t, emitVideoResult(nil, meta, true, &out, &errW))
	assert.Empty(t, errW.String(), "no per-clip status line when nothing was written")

	var raw map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &raw))
	videos, ok := raw["videos"]
	require.True(t, ok)
	assert.Equal(t, []any{}, videos, "must serialize as [] not null")
	assert.Equal(t, "models/m/operations/abc", raw["operation"])
	assert.Equal(t, "abc", raw["job_id"])
	assert.Equal(t, "pending", raw["state"])
}

func TestEmitVideoResultNoPoster(t *testing.T) {
	arts := []videoArtifact{{Path: "/out/clip.mp4", Bytes: 1}}
	var out, errW bytes.Buffer
	require.NoError(t, emitVideoResult(arts, jsonVideoResult{Model: "veo-3.1"}, false, &out, &errW))
	assert.Equal(t, "/out/clip.mp4\n", out.String())
}

func TestArtifactPaths(t *testing.T) {
	arts := []videoArtifact{
		{Path: "/a.mp4", Poster: "/a-poster.png"},
		{Path: "/b.mp4"},
	}
	assert.Equal(t, []string{"/a.mp4", "/a-poster.png", "/b.mp4"}, artifactPaths(arts))
	assert.Nil(t, artifactPaths(nil))
}

func TestVideoCostThreshold(t *testing.T) {
	// A default fast clip must not trip the guard; a standard 1080p clip must.
	fast := videoModelFor(t, "veo-3.1-fast")
	assert.LessOrEqual(t, registry.EstimateVideoCost(fast, "720p", "8"), videoCostThreshold)

	std := videoModelFor(t, "veo-3.1")
	assert.Greater(t, registry.EstimateVideoCost(std, "1080p", "8"), videoCostThreshold)
}

func TestResolveVideoParamsOmni(t *testing.T) {
	m := videoModelFor(t, "omni-flash")

	res, dur, asp, err := resolveVideoParams(m, "", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, "720p", res)
	assert.Equal(t, "5", dur)
	assert.Equal(t, "16:9", asp)

	res, dur, asp, err = resolveVideoParams(m, "360p", "3", "9:16", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"360p", "3", "9:16"}, []string{res, dur, asp})

	_, _, _, err = resolveVideoParams(m, "720p", "12", "", 0)
	assert.ErrorContains(t, err, "duration \"12\" not allowed")
	_, _, _, err = resolveVideoParams(m, "480p", "", "", 0)
	assert.ErrorContains(t, err, "resolution \"480p\" not allowed")
}

func TestResolveEditSource(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(f, []byte("mp4"), 0644))

	var req provider.VideoRequest
	require.NoError(t, resolveEditSource(&req, f))
	assert.Equal(t, []byte("mp4"), req.EditVideo)
	assert.Empty(t, req.PreviousInteraction)

	req = provider.VideoRequest{}
	require.NoError(t, resolveEditSource(&req, "v1_abcDEF-123"))
	assert.Equal(t, "v1_abcDEF-123", req.PreviousInteraction)

	req = provider.VideoRequest{}
	require.NoError(t, resolveEditSource(&req, "interactions/v1_abc"))
	assert.Equal(t, "interactions/v1_abc", req.PreviousInteraction)

	// A path-looking string that is not a file is a typo, not an id.
	err := resolveEditSource(&provider.VideoRequest{}, filepath.Join(dir, "missing.mp4"))
	assert.ErrorContains(t, err, "neither an existing file nor an interaction id")
}

func TestVideoMetaPrefersUsageCost(t *testing.T) {
	job := jobs.Job{Model: "omni-flash", CostEstimateUSD: 0.30}
	m := videoMetaFor(job, jobs.StateFetched, 0)
	assert.Equal(t, provider.CostSourceRegistry, m.CostSource)
	assert.InDelta(t, 0.30, m.CostEstimateUSD, 1e-9)

	job.CostUSD, job.CostSource = 0.11, provider.CostSourceUsage
	m = videoMetaFor(job, jobs.StateFetched, 0)
	assert.Equal(t, provider.CostSourceUsage, m.CostSource)
	assert.InDelta(t, 0.11, m.CostEstimateUSD, 1e-9)
}

// Job records from before kie.ai was removed must get a clear error, not a
// prompt to configure a provider that no longer exists.
func TestVideoProviderForRemovedProvider(t *testing.T) {
	_, err := videoProviderFor("kie")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kie.ai was removed")
	assert.NotContains(t, err.Error(), "config init")
}
