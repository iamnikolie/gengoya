package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveKindModelProviderDefaults(t *testing.T) {
	reg := testRegistry(t)
	// Profile default_provider openai has no music default: falls through to gemini.
	cfg := &config.Config{DefaultProvider: "openai"}

	sel, err := resolveKindModelProvider(reg, cfg, registry.KindMusic, "", "")
	require.NoError(t, err)
	assert.Equal(t, "lyria-3.5", sel.Alias)
	assert.Equal(t, "gemini", sel.Provider)

	sel, err = resolveKindModelProvider(reg, cfg, registry.KindSpeech, "", "gemini")
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.8-flash-tts", sel.Alias)

	sel, err = resolveKindModelProvider(reg, cfg, registry.KindMusic, "lyria-3-clip-preview", "")
	require.NoError(t, err)
	assert.Equal(t, "lyria-3-clip-preview", sel.Alias)
}

func TestResolveKindModelProviderErrors(t *testing.T) {
	reg := testRegistry(t)
	cfg := &config.Config{DefaultProvider: "gemini"}

	_, err := resolveKindModelProvider(reg, cfg, registry.KindMusic, "nano-banana-2", "")
	assert.ErrorContains(t, err, "is an image model, not music")
	_, err = resolveKindModelProvider(reg, cfg, registry.KindSpeech, "lyria-3.5", "")
	assert.ErrorContains(t, err, "is a music model, not speech")
	_, err = resolveKindModelProvider(reg, cfg, registry.KindMusic, "nope", "")
	assert.ErrorContains(t, err, "unknown model alias")
	_, err = resolveKindModelProvider(reg, cfg, registry.KindMusic, "lyria-3.5", "openai")
	assert.ErrorContains(t, err, "belongs to provider")
	_, err = resolveKindModelProvider(reg, cfg, registry.KindSpeech, "", "openai")
	assert.ErrorContains(t, err, "no speech model")
}

func TestVideoResolverNamesMusicKind(t *testing.T) {
	_, err := resolveVideoModelProvider(testRegistry(t), &config.Config{DefaultProvider: "gemini"}, "lyria-3.5", "")
	assert.ErrorContains(t, err, "is a music model, not video")
}

func TestParseSpeakerFlags(t *testing.T) {
	got, err := parseSpeakerFlags([]string{"Joe=Puck", " Jane = Kore "})
	require.NoError(t, err)
	assert.Equal(t, []provider.SpeakerVoice{{Speaker: "Joe", Voice: "Puck"}, {Speaker: "Jane", Voice: "Kore"}}, got)

	for _, bad := range []string{"Joe", "=Puck", "Joe="} {
		_, err = parseSpeakerFlags([]string{bad})
		assert.ErrorContains(t, err, "expected Name=Voice", bad)
	}
	_, err = parseSpeakerFlags([]string{"A=Puck", "A=Kore"})
	assert.ErrorContains(t, err, "twice")
}

func TestParseDialogue(t *testing.T) {
	sp := []provider.SpeakerVoice{{Speaker: "Joe", Voice: "Puck"}, {Speaker: "Jane", Voice: "Kore"}}
	script := `
Joe [cheerful]: Hi Jane! Time: 10:30, ok?
Jane: Fine.
 And you?

Joe: Great <laugh>
`
	turns, err := parseDialogue(script, sp, "calm")
	require.NoError(t, err)
	require.Len(t, turns, 3)
	assert.Equal(t, provider.SpeechTurn{Speaker: "Joe", Text: "Hi Jane! Time: 10:30, ok?", Style: "cheerful"}, turns[0])
	// "Time:" is not a declared speaker: it stays inside the text. Continuation lines
	// join the previous turn; the default style fills turns without their own.
	assert.Equal(t, provider.SpeechTurn{Speaker: "Jane", Text: "Fine. And you?", Style: "calm"}, turns[1])
	assert.Equal(t, provider.SpeechTurn{Speaker: "Joe", Text: "Great <laugh>", Style: "calm"}, turns[2])
}

func TestParseDialogueErrors(t *testing.T) {
	sp := []provider.SpeakerVoice{{Speaker: "Joe", Voice: "Puck"}, {Speaker: "Jane", Voice: "Kore"}}
	_, err := parseDialogue("just some text", sp, "")
	assert.ErrorContains(t, err, "must start with a speaker line")
	assert.ErrorContains(t, err, "Joe, Jane")
	_, err = parseDialogue("  \n\n", sp, "")
	assert.ErrorContains(t, err, "no dialogue turns")
}

func TestReadSpeakText(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "t.txt")
	require.NoError(t, os.WriteFile(f, []byte("  from file \n"), 0644))

	got, err := readSpeakText(nil, f, nil)
	require.NoError(t, err)
	assert.Equal(t, "from file", got)

	got, err = readSpeakText([]string{"inline"}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "inline", got)

	_, err = readSpeakText([]string{"inline"}, f, nil)
	assert.ErrorContains(t, err, "not both")
	_, err = readSpeakText(nil, "", nil)
	assert.ErrorContains(t, err, "text is required")
	_, err = readSpeakText(nil, filepath.Join(dir, "missing"), nil)
	assert.ErrorContains(t, err, "read --file")

	// "-" reads stdin.
	in := filepath.Join(dir, "stdin")
	require.NoError(t, os.WriteFile(in, []byte("piped text\n"), 0644))
	fh, err := os.Open(in)
	require.NoError(t, err)
	defer fh.Close()
	got, err = readSpeakText([]string{"-"}, "", fh)
	require.NoError(t, err)
	assert.Equal(t, "piped text", got)
}

func TestBuildAudioFilenames(t *testing.T) {
	mp3 := provider.Audio{MimeType: "audio/mpeg"}
	wav := provider.Audio{MimeType: "audio/wav"}
	assert.Equal(t, []string{"song.mp3"}, buildAudioFilenames("x", "song", []provider.Audio{mp3}))
	assert.Equal(t, []string{"s-1.mp3", "s-2.wav"}, buildAudioFilenames("x", "s", []provider.Audio{mp3, wav}))
	got := buildAudioFilenames("A bright chiptune melody", "", []provider.Audio{mp3})
	require.Len(t, got, 1)
	assert.Regexp(t, `^a-bright-chiptune-melody-[0-9a-f]{8}\.mp3$`, got[0])
	got = buildAudioFilenames("!!!", "", []provider.Audio{wav})
	assert.Regexp(t, `^audio-[0-9a-f]{8}\.wav$`, got[0])
}

func TestWriteAudioWithSidecarAndDedupe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	files, err := writeAudio(dir, "p", "song", []provider.Audio{{Data: []byte("abc"), MimeType: "audio/mpeg"}}, "[0:00] la")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, filepath.Join(dir, "song.mp3"), files[0].Path)
	assert.Equal(t, filepath.Join(dir, "song.txt"), files[0].Text)
	b, _ := os.ReadFile(files[0].Text)
	assert.Equal(t, "[0:00] la\n", string(b))

	// Same name again: " (1)" inserted, sidecar follows the audio name.
	files, err = writeAudio(dir, "p", "song", []provider.Audio{{Data: []byte("def"), MimeType: "audio/mpeg"}}, "x")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "song (1).mp3"), files[0].Path)
	assert.Equal(t, filepath.Join(dir, "song (1).txt"), files[0].Text)

	files, err = writeAudio(dir, "p", "clip", []provider.Audio{{Data: []byte("w"), MimeType: "audio/wav"}}, "")
	require.NoError(t, err)
	assert.Empty(t, files[0].Text)
}

func TestEmitAudioResultPaths(t *testing.T) {
	var out, errb bytes.Buffer
	files := []writtenAudio{{Path: "/o/a.mp3", Text: "/o/a.txt", Bytes: 10, Mime: "audio/mpeg", Format: "mp3"}}
	meta := audioMeta{Model: "lyria-3.5", APIID: "lyria-3.5", Provider: "gemini", CostUSD: 0.08, CostSource: "registry", ElapsedMS: 5}
	require.NoError(t, emitAudioResult(files, []float64{0}, meta, false, &out, &errb))
	assert.Equal(t, "/o/a.mp3\n/o/a.txt\n", out.String(), "stdout is paths only: audio, then the text sidecar")
	assert.Contains(t, errb.String(), "model=lyria-3.5 id=lyria-3.5 fmt=mp3 bytes=10 cost≈$0.0800 (registry) ms=5")
	assert.NotContains(t, errb.String(), "dur=")
}

func TestEmitAudioResultJSONSpeech(t *testing.T) {
	var out, errb bytes.Buffer
	files := []writtenAudio{{Path: "/o/a.wav", Bytes: 99, Mime: "audio/wav", Format: "wav"}}
	meta := audioMeta{
		Model: "gemini-3.8-flash-tts", APIID: "gemini-3.8-flash-tts", Provider: "gemini", Voice: "Kore",
		Speakers: []string{"Joe=Puck", "Jane=Kore"}, CostUSD: 0.0021, CostSource: "usage", ElapsedMS: 3,
		Usage: &provider.GeminiUsage{PromptTokenCount: 19, CandidatesTokenCount: 236},
	}
	require.NoError(t, emitAudioResult(files, []float64{7.36}, meta, true, &out, &errb))
	assert.Contains(t, errb.String(), "dur=7.4s voice=Kore")
	var got map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	a := got["audio"].([]any)[0].(map[string]any)
	assert.Equal(t, "/o/a.wav", a["path"])
	assert.EqualValues(t, 7.36, a["duration_seconds"])
	assert.NotContains(t, a, "lyrics")
	assert.Equal(t, "usage", got["cost_source"])
	assert.Equal(t, "Kore", got["voice"])
	assert.Equal(t, 1, strings.Count(out.String(), "\n"), "one JSON object")
}
