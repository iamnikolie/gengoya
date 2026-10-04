package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadVideoModel(t *testing.T, alias string) Model {
	t.Helper()
	r, err := Load("")
	require.NoError(t, err)
	m, ok := r.Lookup(alias)
	require.True(t, ok, "alias %s missing from embedded registry", alias)
	return m
}

func TestEmbeddedVideoModels(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)

	alias, ok := r.DefaultVideoFor("gemini")
	require.True(t, ok)
	assert.Equal(t, "veo-3.1-fast", alias)
	assert.Equal(t, []string{"gemini"}, r.VideoProviders())

	_, ok = r.DefaultVideoFor("openai")
	assert.False(t, ok, "openai has no live video model (Sora API shut down)")

	for _, a := range []string{"veo-3.1", "veo-3.1-fast", "veo-3.1-lite"} {
		m := loadVideoModel(t, a)
		assert.True(t, IsVideo(m), "%s should be kind video", a)
		assert.Equal(t, "gemini", m.Provider)
		assert.True(t, SupportsOp(m, "video"))
		assert.NotEmpty(t, m.Resolutions)
		assert.NotEmpty(t, m.Durations)
		assert.NotEmpty(t, m.PricePerSec)
	}

	// Image models keep the default kind.
	img := loadVideoModel(t, "gpt-image-2")
	assert.Equal(t, KindImage, img.Kind)
	assert.False(t, IsVideo(img))
}

func TestVideoDefaults(t *testing.T) {
	m := loadVideoModel(t, "veo-3.1-fast")
	assert.Equal(t, "720p", DefaultResolution(m))
	assert.Equal(t, "8", DefaultDuration(m))
}

func TestValidateResolutionDuration(t *testing.T) {
	m := loadVideoModel(t, "veo-3.1-fast")
	assert.NoError(t, ValidateResolution(m, "1080p"))
	assert.NoError(t, ValidateResolution(m, ""))
	assert.Error(t, ValidateResolution(m, "8k"))

	assert.NoError(t, ValidateDuration(m, "6"))
	assert.NoError(t, ValidateDuration(m, ""))
	assert.Error(t, ValidateDuration(m, "12"))

	lite := loadVideoModel(t, "veo-3.1-lite")
	assert.Error(t, ValidateResolution(lite, "4k"), "lite has no 4k tier")
}

func TestValidateVideoCombo(t *testing.T) {
	m := loadVideoModel(t, "veo-3.1")

	// 1080p and 4k accept only 8s.
	assert.NoError(t, ValidateVideoCombo(m, "1080p", "8", 0))
	assert.Error(t, ValidateVideoCombo(m, "1080p", "4", 0))
	assert.Error(t, ValidateVideoCombo(m, "4k", "6", 0))
	// 720p is free to pick.
	assert.NoError(t, ValidateVideoCombo(m, "720p", "4", 0))
	// Omitted duration is resolved later, not rejected here.
	assert.NoError(t, ValidateVideoCombo(m, "1080p", "", 0))

	// Reference images: capped and duration-locked.
	assert.NoError(t, ValidateVideoCombo(m, "720p", "8", 3))
	assert.Error(t, ValidateVideoCombo(m, "720p", "8", 4))
	assert.Error(t, ValidateVideoCombo(m, "720p", "4", 1))
}

func TestResolveVideoDuration(t *testing.T) {
	m := loadVideoModel(t, "veo-3.1")

	assert.Equal(t, "4", ResolveVideoDuration(m, "720p", "4", 0), "explicit wins")
	assert.Equal(t, "8", ResolveVideoDuration(m, "1080p", "", 0), "resolution forces 8")
	assert.Equal(t, "8", ResolveVideoDuration(m, "4k", "", 0))
	assert.Equal(t, "8", ResolveVideoDuration(m, "720p", "", 2), "refs force 8")
	assert.Equal(t, "8", ResolveVideoDuration(m, "720p", "", 0), "model default")
}

func TestVideoPricing(t *testing.T) {
	fast := loadVideoModel(t, "veo-3.1-fast")
	assert.InDelta(t, 0.10, VideoPricePerSec(fast, "720p"), 1e-9)
	assert.InDelta(t, 0.12, VideoPricePerSec(fast, "1080p"), 1e-9)
	assert.InDelta(t, 0.30, VideoPricePerSec(fast, "4k"), 1e-9)
	// Unknown resolution falls back to the most expensive declared rate.
	assert.InDelta(t, 0.30, VideoPricePerSec(fast, "8k"), 1e-9)

	assert.InDelta(t, 0.80, EstimateVideoCost(fast, "720p", "8"), 1e-9)
	assert.InDelta(t, 0.40, EstimateVideoCost(fast, "720p", "4"), 1e-9)

	std := loadVideoModel(t, "veo-3.1")
	assert.InDelta(t, 3.20, EstimateVideoCost(std, "1080p", "8"), 1e-9)
	assert.InDelta(t, 4.80, EstimateVideoCost(std, "4k", "8"), 1e-9)

	lite := loadVideoModel(t, "veo-3.1-lite")
	assert.InDelta(t, 0.40, EstimateVideoCost(lite, "720p", "8"), 1e-9)

	// Bad duration → no estimate rather than a bogus number.
	assert.Zero(t, EstimateVideoCost(std, "720p", "abc"))
	assert.Zero(t, EstimateVideoCost(std, "720p", ""))
	// Image models have no per-second price.
	assert.Zero(t, VideoPricePerSec(loadVideoModel(t, "gpt-image-2"), "720p"))
}

func TestVideoAspectValidation(t *testing.T) {
	m := loadVideoModel(t, "veo-3.1")
	assert.NoError(t, ValidateAspect(m, "16:9"))
	assert.NoError(t, ValidateAspect(m, "9:16"))
	assert.Error(t, ValidateAspect(m, "1:1"), "Veo is landscape/portrait only")
}

func TestEnumDurationValidation(t *testing.T) {
	veo := loadVideoModel(t, "veo-3.1")
	for _, d := range []string{"4", "6", "8", ""} {
		assert.NoError(t, ValidateDuration(veo, d))
	}
	assert.Error(t, ValidateDuration(veo, "5"))
	assert.Error(t, ValidateDuration(veo, "abc"))
}

func TestVideoRegistryOverride(t *testing.T) {
	// A profile registry.yaml can add a video model without a rebuild.
	data := []byte(`
video_defaults:
  gemini: my-veo
models:
  my-veo:
    provider: gemini
    kind: video
    api_id: veo-9-preview
    ops: [video]
    resolutions: [720p]
    durations: ["5"]
    aspect_ratios: ["16:9"]
    price_per_sec:
      720p: 0.01
`)
	r, err := Parse(data)
	require.NoError(t, err)
	m, ok := r.Lookup("my-veo")
	require.True(t, ok)
	assert.True(t, IsVideo(m))
	assert.Equal(t, "veo-9-preview", m.APIID)
	assert.InDelta(t, 0.05, EstimateVideoCost(m, "720p", "5"), 1e-9)
	alias, ok := r.DefaultVideoFor("gemini")
	require.True(t, ok)
	assert.Equal(t, "my-veo", alias)
}

func TestOmniFlashRegistry(t *testing.T) {
	m := loadVideoModel(t, "omni-flash")
	assert.Equal(t, "gemini", m.Provider)
	assert.Equal(t, "gemini-omni-1.1-flash", m.APIID, "GA id; the deprecated -preview id is not registered")
	assert.True(t, IsVideo(m))
	assert.True(t, SupportsOp(m, "video"))

	// Gemini default video stays Veo.
	def, _ := mustLoad(t).DefaultVideoFor("gemini")
	assert.Equal(t, "veo-3.1-fast", def)

	assert.Equal(t, "720p", DefaultResolution(m))
	assert.Equal(t, "5", DefaultDuration(m))
	for _, d := range []string{"3", "4", "5", "6", "7", "8", "9", "10"} {
		assert.NoError(t, ValidateDuration(m, d), d)
	}
	assert.Error(t, ValidateDuration(m, "2"), "min 3s verified live")
	assert.Error(t, ValidateDuration(m, "11"), "max 10s verified live")
	for _, r := range []string{"360p", "720p", "1080p", "4k"} {
		assert.NoError(t, ValidateResolution(m, r), r)
	}
	assert.Error(t, ValidateResolution(m, "480p"))
	assert.NoError(t, ValidateAspect(m, "9:16"))
	assert.Error(t, ValidateAspect(m, "1:1"))

	// No cross-field duration constraints (unlike Veo).
	assert.NoError(t, ValidateVideoCombo(m, "1080p", "3", 0))
	assert.NoError(t, ValidateVideoCombo(m, "720p", "3", 2))
}

func TestOmniFlashPricing(t *testing.T) {
	m := loadVideoModel(t, "omni-flash")
	// Measured live 2026-10-04 (video tokens at $17.50/1M):
	// 360p 5,793 tok / 3s, 720p 17,376 / 3s, 1080p 26,064 / 3s.
	assert.InDelta(t, 5793.0/3*17.5/1e6, VideoPricePerSec(m, "360p"), 0.001)
	assert.InDelta(t, 5792*17.5/1e6, VideoPricePerSec(m, "720p"), 0.001)
	assert.InDelta(t, 8688*17.5/1e6, VideoPricePerSec(m, "1080p"), 0.001)
	assert.Greater(t, VideoPricePerSec(m, "4k"), VideoPricePerSec(m, "1080p"))
	assert.InDelta(t, 0.304, EstimateVideoCost(m, "720p", "3"), 0.01)
}

func mustLoad(t *testing.T) *Registry {
	t.Helper()
	r, err := Load("")
	require.NoError(t, err)
	return r
}
