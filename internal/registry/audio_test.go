package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedMusicModels(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)

	want := map[string]struct {
		price float64
		fixed int
	}{
		"lyria-3.5":            {0.08, 0},
		"lyria-3-clip-preview": {0.04, 30},
		"lyria-3-pro-preview":  {0.08, 0},
	}
	for alias, w := range want {
		m, ok := r.Lookup(alias)
		require.True(t, ok, alias)
		assert.Equal(t, KindMusic, m.Kind)
		assert.True(t, IsMusic(m))
		assert.Equal(t, "gemini", m.Provider)
		assert.Equal(t, alias, m.APIID)
		assert.True(t, SupportsOp(m, "music"))
		assert.InDelta(t, w.price, m.PricePerSong, 1e-9, alias)
		assert.Equal(t, w.fixed, m.FixedSeconds, alias)
		assert.Equal(t, 10, m.MaxRefImages)
		assert.Equal(t, "mp3", DefaultFormat(m))
	}
	def, ok := r.DefaultKindFor(KindMusic, "gemini")
	require.True(t, ok)
	assert.Equal(t, "lyria-3.5", def)
	assert.Equal(t, []string{"gemini"}, r.KindProviders(KindMusic))
}

func TestEmbeddedSpeechModels(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)

	for alias, audioOut := range map[string]float64{"gemini-3.8-flash-tts": 9.0, "gemini-3.8-flash-lite-tts": 6.0} {
		m, ok := r.Lookup(alias)
		require.True(t, ok, alias)
		assert.Equal(t, KindSpeech, m.Kind)
		assert.True(t, IsSpeech(m))
		assert.True(t, SupportsOp(m, "speak"))
		assert.InDelta(t, 0.50, m.PriceTextIn, 1e-9)
		assert.InDelta(t, audioOut, m.PriceAudioOut, 1e-9)
		assert.Equal(t, 2, m.MaxSpeakers)
		assert.Equal(t, "Kore", m.DefaultVoice)
		assert.Len(t, m.Voices, 30, "the YAML anchor shares the 30 prebuilt voices")
		assert.Contains(t, m.Voices, "Puck")
	}
	def, ok := r.DefaultKindFor(KindSpeech, "gemini")
	require.True(t, ok)
	assert.Equal(t, "gemini-3.8-flash-tts", def)
	_, ok = r.DefaultKindFor(KindSpeech, "openai")
	assert.False(t, ok)
}

func TestMusicSpeechDoNotLeakIntoImageDefaults(t *testing.T) {
	r, err := Load("")
	require.NoError(t, err)
	d, _ := r.DefaultFor("gemini")
	assert.Equal(t, "nano-banana-2", d)
	v, _ := r.DefaultVideoFor("gemini")
	assert.Equal(t, "veo-3.1-fast", v)
}

func TestValidateFormat(t *testing.T) {
	m := Model{APIID: "x", Formats: []string{"mp3"}}
	assert.NoError(t, ValidateFormat(m, ""))
	assert.NoError(t, ValidateFormat(m, "MP3"))
	assert.ErrorContains(t, ValidateFormat(m, "wav"), "allowed: mp3")
	assert.Equal(t, "", DefaultFormat(Model{}))
}

func TestEstimateSpeechCost(t *testing.T) {
	m := Model{PriceTextIn: 0.5, PriceAudioOut: 9, AudioTokensPerSec: 32}
	assert.InDelta(t, (100*0.5+10*32*9.0)/1e6, EstimateSpeechCost(m, 100, 10), 1e-12)
	// Unset tokens/sec falls back to the documented 25.
	m.AudioTokensPerSec = 0
	assert.InDelta(t, (10*25*9.0)/1e6, EstimateSpeechCost(m, 0, 10), 1e-12)
}
