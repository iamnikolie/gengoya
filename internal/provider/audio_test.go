package provider

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/iamnikolie/gengoya/internal/client"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lyriaModel() registry.Model {
	return registry.Model{
		Provider: "gemini", Kind: registry.KindMusic, APIID: "lyria-3.5", Ops: []string{"music"},
		Formats: []string{"mp3"}, MaxRefImages: 10, PricePerSong: 0.08,
	}
}

func clipModel() registry.Model {
	m := lyriaModel()
	m.APIID, m.FixedSeconds, m.PricePerSong = "lyria-3-clip-preview", 30, 0.04
	return m
}

func ttsModel() registry.Model {
	return registry.Model{
		Provider: "gemini", Kind: registry.KindSpeech, APIID: "gemini-3.8-flash-tts", Ops: []string{"speak"},
		DefaultVoice: "Kore", MaxSpeakers: 2, PriceTextIn: 0.50, PriceAudioOut: 9.0, AudioTokensPerSec: 32,
	}
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// ---- music -----------------------------------------------------------------

func TestMusicPrompt(t *testing.T) {
	p, err := MusicPrompt(MusicRequest{Model: lyriaModel(), Prompt: "folk song"})
	require.NoError(t, err)
	assert.Equal(t, "folk song", p)

	p, err = MusicPrompt(MusicRequest{Model: lyriaModel(), Prompt: "folk song", Instrumental: true, DurationSecs: 90})
	require.NoError(t, err)
	assert.Equal(t, "folk song\n\nInstrumental only, no vocals.\n\nThe song should be about 90 seconds long.", p)

	p, err = MusicPrompt(MusicRequest{Model: lyriaModel(), Prompt: "pop", Lyrics: "[Verse]\nla la"})
	require.NoError(t, err)
	assert.Equal(t, "pop\n\nLyrics:\n[Verse]\nla la", p)
}

func TestMusicPromptErrors(t *testing.T) {
	_, err := MusicPrompt(MusicRequest{Model: lyriaModel()})
	assert.ErrorContains(t, err, "required")
	_, err = MusicPrompt(MusicRequest{Model: lyriaModel(), Prompt: "x", Instrumental: true, Lyrics: "la"})
	assert.ErrorContains(t, err, "mutually exclusive")
	_, err = MusicPrompt(MusicRequest{Model: clipModel(), Prompt: "x", DurationSecs: 60})
	assert.ErrorContains(t, err, "always generates 30 s")
	_, err = MusicPrompt(MusicRequest{Model: lyriaModel(), Prompt: "x", DurationSecs: -1})
	assert.ErrorContains(t, err, "positive")
}

func TestBuildMusicRequestPlain(t *testing.T) {
	b, err := BuildMusicRequest(MusicRequest{Model: lyriaModel(), Prompt: "folk song"})
	require.NoError(t, err)
	body := decodeJSON(t, b)
	parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 1)
	assert.Equal(t, "folk song", parts[0].(map[string]any)["text"])
	assert.NotContains(t, body, "generationConfig", "default mp3 needs no generationConfig")
}

func TestBuildMusicRequestImages(t *testing.T) {
	b, err := BuildMusicRequest(MusicRequest{
		Model: lyriaModel(), Prompt: "mood", Images: [][]byte{{1, 2}, {3}}, ImageNames: []string{"a.jpg", "b.png"},
	})
	require.NoError(t, err)
	parts := decodeJSON(t, b)["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 3)
	// Text first, then images (the documented order).
	assert.Equal(t, "mood", parts[0].(map[string]any)["text"])
	in := parts[1].(map[string]any)["inline_data"].(map[string]any)
	assert.Equal(t, "image/jpeg", in["mime_type"])
	assert.Equal(t, b64([]byte{1, 2}), in["data"])

	m := lyriaModel()
	m.MaxRefImages = 1
	_, err = BuildMusicRequest(MusicRequest{Model: m, Prompt: "x", Images: [][]byte{{1}, {2}}})
	assert.ErrorContains(t, err, "at most 1 images")
	m.MaxRefImages = 0
	_, err = BuildMusicRequest(MusicRequest{Model: m, Prompt: "x", Images: [][]byte{{1}}})
	assert.ErrorContains(t, err, "does not take image input")
}

func TestBuildMusicRequestFormat(t *testing.T) {
	_, err := BuildMusicRequest(MusicRequest{Model: lyriaModel(), Prompt: "x", Format: "wav"})
	assert.ErrorContains(t, err, "not allowed")

	// When a registry override re-enables wav the request carries the enum value
	// live-verified 2026-10-04 ("audio/wav" is a 400; "AUDIO_WAV" is accepted).
	m := lyriaModel()
	m.Formats = []string{"mp3", "wav"}
	b, err := BuildMusicRequest(MusicRequest{Model: m, Prompt: "x", Format: "wav"})
	require.NoError(t, err)
	gc := decodeJSON(t, b)["generationConfig"].(map[string]any)
	assert.Equal(t, []any{"AUDIO", "TEXT"}, gc["responseModalities"])
	assert.Equal(t, "AUDIO_WAV", gc["responseFormat"].(map[string]any)["audio"].(map[string]any)["mimeType"])
}

func musicResponse(parts ...map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"parts": parts}, "finishReason": "STOP",
	}}})
	return b
}

func TestParseMusicResponseOrderIndependent(t *testing.T) {
	audio := []byte("ID3fake-mp3")
	// Audio before text: the docs warn lyrics are not always the first part.
	res, err := ParseMusicResponse(musicResponse(
		map[string]any{"inlineData": map[string]any{"mimeType": "audio/mpeg", "data": b64(audio)}},
		map[string]any{"text": "[0.0:5.0] la la"},
	), MusicRequest{Model: clipModel()})
	require.NoError(t, err)
	require.Len(t, res.Tracks, 1)
	assert.Equal(t, audio, res.Tracks[0].Data)
	assert.Equal(t, "audio/mpeg", res.Tracks[0].MimeType)
	assert.Equal(t, "[0.0:5.0] la la", res.Text)
	assert.InDelta(t, 0.04, res.CostUSD, 1e-9)
	assert.Equal(t, CostSourceRegistry, res.CostSource)
}

func TestParseMusicResponseInstrumentalSentinel(t *testing.T) {
	res, err := ParseMusicResponse(musicResponse(
		map[string]any{"text": "<instrumental>"},
		map[string]any{"inlineData": map[string]any{"mimeType": "audio/mpeg", "data": b64([]byte("x"))}},
	), MusicRequest{Model: lyriaModel()})
	require.NoError(t, err)
	assert.Empty(t, res.Text, "<instrumental> marker is not lyrics")
}

func TestParseMusicResponseNoAudio(t *testing.T) {
	_, err := ParseMusicResponse(musicResponse(map[string]any{"text": "I cannot do that"}), MusicRequest{Model: lyriaModel()})
	assert.ErrorContains(t, err, "no audio in response: I cannot do that")

	blocked := []byte(`{"candidates":[{"finishReason":"SAFETY","content":{"parts":[]}}]}`)
	_, err = ParseMusicResponse(blocked, MusicRequest{Model: lyriaModel()})
	assert.ErrorContains(t, err, "finishReason=SAFETY")

	_, err = ParseMusicResponse([]byte(`{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`), MusicRequest{Model: lyriaModel()})
	assert.ErrorContains(t, err, "blockReason=PROHIBITED_CONTENT")
}

// ---- speech ----------------------------------------------------------------

func TestBuildSpeechRequestSingle(t *testing.T) {
	b, err := BuildSpeechRequest(SpeechRequest{
		Model: ttsModel(), Voice: "Puck", Turns: []SpeechTurn{{Text: "Hello", Style: "warm"}},
	})
	require.NoError(t, err)
	body := decodeJSON(t, b)
	c := body["contents"].([]any)[0].(map[string]any)
	assert.Equal(t, "user", c["role"])
	p := c["parts"].([]any)[0].(map[string]any)
	assert.Equal(t, "Hello", p["text"], "text stays the verbatim transcript; style is not folded in")
	assert.Equal(t, map[string]any{"style": "warm"}, p["speech_metadata"])
	gc := body["generationConfig"].(map[string]any)
	assert.Equal(t, []any{"AUDIO"}, gc["responseModalities"])
	assert.Equal(t, "Puck", gc["speechConfig"].(map[string]any)["voiceConfig"].(map[string]any)["voice"])
	assert.NotContains(t, gc, "responseFormat", "default is the API's complete WAV")
}

func TestBuildSpeechRequestDefaultsAndNoStyle(t *testing.T) {
	b, err := BuildSpeechRequest(SpeechRequest{Model: ttsModel(), Turns: []SpeechTurn{{Text: "Hi"}}})
	require.NoError(t, err)
	body := decodeJSON(t, b)
	p := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
	assert.NotContains(t, p, "speech_metadata")
	assert.Equal(t, "Kore", body["generationConfig"].(map[string]any)["speechConfig"].(map[string]any)["voiceConfig"].(map[string]any)["voice"])
}

func TestBuildSpeechRequestMulti(t *testing.T) {
	b, err := BuildSpeechRequest(SpeechRequest{
		Model:    ttsModel(),
		Speakers: []SpeakerVoice{{"Joe", "Puck"}, {"Jane", "Kore"}},
		Turns:    []SpeechTurn{{Speaker: "Joe", Text: "Hi", Style: "cheerful"}, {Speaker: "Jane", Text: "Yo"}},
	})
	require.NoError(t, err)
	body := decodeJSON(t, b)
	parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 2)
	assert.Equal(t, map[string]any{"speaker": "Joe", "style": "cheerful"}, parts[0].(map[string]any)["speech_metadata"])
	assert.Equal(t, map[string]any{"speaker": "Jane"}, parts[1].(map[string]any)["speech_metadata"])
	sc := body["generationConfig"].(map[string]any)["speechConfig"].(map[string]any)
	assert.NotContains(t, sc, "voiceConfig")
	cfgs := sc["multiSpeakerVoiceConfig"].(map[string]any)["speakerVoiceConfigs"].([]any)
	require.Len(t, cfgs, 2)
	first := cfgs[0].(map[string]any)
	assert.Equal(t, "Joe", first["speaker"])
	assert.Equal(t, "Puck", first["voiceConfig"].(map[string]any)["prebuiltVoiceConfig"].(map[string]any)["voiceName"])
}

func TestBuildSpeechRequestErrors(t *testing.T) {
	m := ttsModel()
	_, err := BuildSpeechRequest(SpeechRequest{Model: m})
	assert.ErrorContains(t, err, "no text")
	_, err = BuildSpeechRequest(SpeechRequest{Model: m, Turns: []SpeechTurn{{Text: "  "}}})
	assert.ErrorContains(t, err, "empty text")
	// The API itself demands exactly 2 (live: "must equal 2").
	_, err = BuildSpeechRequest(SpeechRequest{Model: m, Speakers: []SpeakerVoice{{"A", "Puck"}}, Turns: []SpeechTurn{{Speaker: "A", Text: "x"}}})
	assert.ErrorContains(t, err, "needs 2")
	three := []SpeakerVoice{{"A", "Puck"}, {"B", "Kore"}, {"C", "Zephyr"}}
	_, err = BuildSpeechRequest(SpeechRequest{Model: m, Speakers: three, Turns: []SpeechTurn{{Speaker: "A", Text: "x"}}})
	assert.ErrorContains(t, err, "at most 2")
	two := []SpeakerVoice{{"A", "Puck"}, {"B", "Kore"}}
	_, err = BuildSpeechRequest(SpeechRequest{Model: m, Speakers: two, Turns: []SpeechTurn{{Speaker: "Z", Text: "x"}}})
	assert.ErrorContains(t, err, `"Z" has no --speaker voice`)
	m.DefaultVoice = ""
	_, err = BuildSpeechRequest(SpeechRequest{Model: m, Turns: []SpeechTurn{{Text: "x"}}})
	assert.ErrorContains(t, err, "no voice")
}

func TestBuildSpeechRequestFormat(t *testing.T) {
	b, err := BuildSpeechRequest(SpeechRequest{Model: ttsModel(), Turns: []SpeechTurn{{Text: "x"}}, Format: "pcm"})
	require.NoError(t, err)
	af := decodeJSON(t, b)["generationConfig"].(map[string]any)["responseFormat"].(map[string]any)["audio"].(map[string]any)
	assert.Equal(t, "AUDIO_L16", af["mimeType"])
	assert.NotContains(t, af, "sampleRate", "the API ignores sampleRate, so it is never sent")

	b, err = BuildSpeechRequest(SpeechRequest{Model: ttsModel(), Turns: []SpeechTurn{{Text: "x"}}, Format: "wav"})
	require.NoError(t, err)
	assert.NotContains(t, decodeJSON(t, b)["generationConfig"], "responseFormat")
}

func speechResponse(mime string, data []byte, usage map[string]any) []byte {
	m := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
		map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": b64(data)}},
	}}}}}
	if usage != nil {
		m["usageMetadata"] = usage
	}
	b, _ := json.Marshal(m)
	return b
}

func pcmSecond(rate int, secs float64) []byte { return make([]byte, int(float64(rate)*2*secs)) }

func TestParseSpeechResponseWAVPassthrough(t *testing.T) {
	wav := WrapPCMAsWAV(pcmSecond(24000, 2), 24000, 1, 16)
	res, err := ParseSpeechResponse(speechResponse("audio/wav", wav, nil), SpeechRequest{Model: ttsModel()})
	require.NoError(t, err)
	assert.Equal(t, wav, res.Audio.Data, "a complete WAV is written untouched")
	assert.InDelta(t, 2.0, res.Audio.Seconds, 1e-6)
}

func TestParseSpeechResponseWrapsL16(t *testing.T) {
	pcm := pcmSecond(24000, 1)
	res, err := ParseSpeechResponse(speechResponse("audio/L16;codec=pcm;rate=24000", pcm, nil), SpeechRequest{Model: ttsModel()})
	require.NoError(t, err)
	assert.Equal(t, "audio/wav", res.Audio.MimeType)
	assert.True(t, IsWAV(res.Audio.Data))
	assert.Equal(t, append(WrapPCMAsWAV(nil, 24000, 1, 16), pcm...)[44:], res.Audio.Data[44:])
	assert.InDelta(t, 1.0, res.Audio.Seconds, 1e-6)

	// Rate comes from the mime, falling back to 24 kHz.
	res, err = ParseSpeechResponse(speechResponse("audio/l16; rate=16000; channels=1", pcmSecond(16000, 1), nil), SpeechRequest{Model: ttsModel()})
	require.NoError(t, err)
	assert.EqualValues(t, 16000, binary.LittleEndian.Uint32(res.Audio.Data[24:28]))
	res, err = ParseSpeechResponse(speechResponse("audio/l16", pcmSecond(24000, 1), nil), SpeechRequest{Model: ttsModel()})
	require.NoError(t, err)
	assert.EqualValues(t, 24000, binary.LittleEndian.Uint32(res.Audio.Data[24:28]))
}

func TestParseSpeechResponseUnsupportedMime(t *testing.T) {
	_, err := ParseSpeechResponse(speechResponse("audio/basic", []byte("xxxx"), nil), SpeechRequest{Model: ttsModel()})
	assert.ErrorContains(t, err, "unsupported audio mime")
}

func TestParseSpeechResponseCostFromUsage(t *testing.T) {
	// Real usage from the live 2026-10-04 flash call: 19 text in, 236 audio out.
	usage := map[string]any{
		"promptTokenCount": 19, "candidatesTokenCount": 236, "totalTokenCount": 255,
		"promptTokensDetails":     []any{map[string]any{"modality": "TEXT", "tokenCount": 19}},
		"candidatesTokensDetails": []any{map[string]any{"modality": "AUDIO", "tokenCount": 236}},
	}
	wav := WrapPCMAsWAV(pcmSecond(24000, 7.36), 24000, 1, 16)
	res, err := ParseSpeechResponse(speechResponse("audio/wav", wav, usage), SpeechRequest{Model: ttsModel()})
	require.NoError(t, err)
	assert.Equal(t, CostSourceUsage, res.CostSource)
	assert.InDelta(t, (19*0.50+236*9.0)/1e6, res.CostUSD, 1e-12)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 236, res.Usage.CandidatesTokenCount)
}

func TestParseSpeechResponseCostFallsBackToDuration(t *testing.T) {
	wav := WrapPCMAsWAV(pcmSecond(24000, 10), 24000, 1, 16)
	res, err := ParseSpeechResponse(speechResponse("audio/wav", wav, nil), SpeechRequest{
		Model: ttsModel(), Turns: []SpeechTurn{{Text: "abcdefgh"}}, // 2 text tokens
	})
	require.NoError(t, err)
	assert.Equal(t, CostSourceRegistry, res.CostSource)
	assert.InDelta(t, (2*0.50+10*32*9.0)/1e6, res.CostUSD, 1e-12)
}

func TestSpeechCostFromUsageTotalsOnly(t *testing.T) {
	c, ok := SpeechCostFromUsage(ttsModel(), &GeminiUsage{PromptTokenCount: 10, CandidatesTokenCount: 100})
	require.True(t, ok)
	assert.InDelta(t, (10*0.50+100*9.0)/1e6, c, 1e-12)
	_, ok = SpeechCostFromUsage(ttsModel(), nil)
	assert.False(t, ok)
	_, ok = SpeechCostFromUsage(ttsModel(), &GeminiUsage{})
	assert.False(t, ok)
}

// ---- wav -------------------------------------------------------------------

func TestWrapPCMAsWAVHeader(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	w := WrapPCMAsWAV(pcm, 24000, 1, 16)
	require.Len(t, w, 44+len(pcm))
	assert.Equal(t, "RIFF", string(w[0:4]))
	assert.EqualValues(t, 36+len(pcm), binary.LittleEndian.Uint32(w[4:8]))
	assert.Equal(t, "WAVEfmt ", string(w[8:16]))
	assert.EqualValues(t, 16, binary.LittleEndian.Uint32(w[16:20]))
	assert.EqualValues(t, 1, binary.LittleEndian.Uint16(w[20:22]), "PCM")
	assert.EqualValues(t, 1, binary.LittleEndian.Uint16(w[22:24]))
	assert.EqualValues(t, 24000, binary.LittleEndian.Uint32(w[24:28]))
	assert.EqualValues(t, 48000, binary.LittleEndian.Uint32(w[28:32]), "byte rate")
	assert.EqualValues(t, 2, binary.LittleEndian.Uint16(w[32:34]), "block align")
	assert.EqualValues(t, 16, binary.LittleEndian.Uint16(w[34:36]))
	assert.Equal(t, "data", string(w[36:40]))
	assert.EqualValues(t, len(pcm), binary.LittleEndian.Uint32(w[40:44]))
	assert.Equal(t, pcm, w[44:])

	st := WrapPCMAsWAV(pcm, 44100, 2, 16)
	assert.EqualValues(t, 176400, binary.LittleEndian.Uint32(st[28:32]))
	assert.EqualValues(t, 4, binary.LittleEndian.Uint16(st[32:34]))
}

func TestWAVSeconds(t *testing.T) {
	assert.InDelta(t, 1.5, WAVSeconds(WrapPCMAsWAV(pcmSecond(24000, 1.5), 24000, 1, 16)), 1e-6)
	assert.Zero(t, WAVSeconds([]byte("not a wav at all")))
	assert.Zero(t, WAVSeconds(nil))

	// Streaming writers leave data size 0xFFFFFFFF: take the rest of the file.
	w := WrapPCMAsWAV(pcmSecond(24000, 1), 24000, 1, 16)
	binary.LittleEndian.PutUint32(w[40:44], 0xFFFFFFFF)
	assert.InDelta(t, 1.0, WAVSeconds(w), 1e-6)

	// An extra chunk (LIST) before data is skipped.
	list := append([]byte("LIST"), 4, 0, 0, 0, 'a', 'b', 'c', 'd')
	w = WrapPCMAsWAV(pcmSecond(24000, 1), 24000, 1, 16)
	w2 := append(append(append([]byte{}, w[:36]...), list...), w[36:]...)
	assert.InDelta(t, 1.0, WAVSeconds(w2), 1e-6)
}

func TestParsePCMMime(t *testing.T) {
	r, c := ParsePCMMime("audio/L16;codec=pcm;rate=24000")
	assert.Equal(t, 24000, r)
	assert.Equal(t, 1, c)
	r, c = ParsePCMMime("audio/l16; rate=44100; channels=2")
	assert.Equal(t, 44100, r)
	assert.Equal(t, 2, c)
	r, c = ParsePCMMime("audio/l16")
	assert.Equal(t, 0, r)
	assert.Equal(t, 1, c)
}

func TestAudioExtForMime(t *testing.T) {
	assert.Equal(t, "mp3", AudioExtForMime("audio/mpeg"))
	assert.Equal(t, "wav", AudioExtForMime("audio/wav"))
	assert.Equal(t, "wav", AudioExtForMime("Audio/X-WAV; rate=1"))
	assert.Equal(t, "ogg", AudioExtForMime("audio/ogg"))
	assert.Equal(t, "opus", AudioExtForMime("audio/x-opus"))
	assert.Equal(t, "bin", AudioExtForMime(""))
}

// ---- transport -------------------------------------------------------------

func TestSpeakAndMusicNeverRetry5xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		assert.Equal(t, "secret-key", r.Header.Get("x-goog-api-key"))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	hc := client.New()
	hc.RetryWait = 1
	g := NewGemini("secret-key", hc)
	g.Base = srv.URL

	_, err := g.Speak(context.Background(), SpeechRequest{Model: ttsModel(), Turns: []SpeechTurn{{Text: "x"}}})
	require.Error(t, err)
	assert.ErrorContains(t, err, "not retried")
	assert.EqualValues(t, 1, atomic.LoadInt32(&hits), "a billed TTS call must not be re-POSTed on 5xx")

	_, err = g.GenerateMusic(context.Background(), MusicRequest{Model: lyriaModel(), Prompt: "x"})
	require.Error(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(&hits), "one POST per music call")
}

func TestSpeakEndToEnd(t *testing.T) {
	wav := WrapPCMAsWAV(pcmSecond(24000, 1), 24000, 1, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models/gemini-3.8-flash-tts:generateContent", r.URL.Path)
		_, _ = w.Write(speechResponse("audio/wav", wav, nil))
	}))
	defer srv.Close()
	g := NewGemini("k", client.New())
	g.Base = srv.URL
	res, err := g.Speak(context.Background(), SpeechRequest{Model: ttsModel(), Turns: []SpeechTurn{{Text: "x"}}})
	require.NoError(t, err)
	assert.Equal(t, wav, res.Audio.Data)
}

func TestClientErrorHasNoBilledWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad voice"}`))
	}))
	defer srv.Close()
	g := NewGemini("k", client.New())
	g.Base = srv.URL
	_, err := g.Speak(context.Background(), SpeechRequest{Model: ttsModel(), Turns: []SpeechTurn{{Text: "x"}}})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "billed")
}

func TestWrapPCMAsWAVClampsHeaderFields(t *testing.T) {
	w := WrapPCMAsWAV(make([]byte, 48000), 1<<40, 1<<20, 7)
	require.True(t, IsWAV(w))
	assert.InDelta(t, 1.0, WAVSeconds(w), 1e-9, "out-of-range values fall back to 24 kHz mono 16-bit")
}
