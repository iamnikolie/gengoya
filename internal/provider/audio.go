package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iamnikolie/gengoya/internal/registry"
)

// Audio is one returned audio payload (a song or a speech clip).
type Audio struct {
	Data     []byte
	MimeType string  // e.g. audio/mpeg, audio/wav
	Seconds  float64 // best-known duration (WAV only; 0 = unknown)
}

// MusicRequest is a Lyria generation request. The API has no duration,
// negative-prompt or seed field: everything but images and output format is
// steered through prompt text, assembled by MusicPrompt.
type MusicRequest struct {
	Model        registry.Model
	Prompt       string
	Lyrics       string
	Instrumental bool
	DurationSecs int      // 0 = model decides; refused for fixed-length models
	Images       [][]byte // optional image inputs (image-to-music)
	ImageNames   []string
	Format       string // "" or "mp3" = API default; "wav" = responseFormat audio/wav
}

// MusicResult is the parsed Lyria response.
type MusicResult struct {
	Tracks     []Audio
	Text       string // lyrics / structure the model returned ("" for instrumental)
	CostUSD    float64
	CostSource string
}

// SpeechTurn is one dialogue turn. Speaker is empty for single-voice speech.
type SpeechTurn struct {
	Speaker string
	Text    string
	Style   string
}

// SpeakerVoice maps a dialogue speaker name to a voice.
type SpeakerVoice struct {
	Speaker string
	Voice   string
}

// SpeechRequest is a Gemini TTS request. One turn with no speakers is single
// voice (Voice); two or more SpeakerVoices make it a multi-speaker request.
type SpeechRequest struct {
	Model    registry.Model
	Turns    []SpeechTurn
	Voice    string
	Speakers []SpeakerVoice
	Format   string // "" or "wav" = API default (WAV); "pcm" = request AUDIO_L16, gengoya re-wraps to WAV
}

// SpeechResult is the parsed TTS response; Audio is always a complete WAV.
type SpeechResult struct {
	Audio      Audio
	CostUSD    float64
	CostSource string
	Usage      *GeminiUsage
}

// MusicProvider generates music.
type MusicProvider interface {
	GenerateMusic(ctx context.Context, r MusicRequest) (MusicResult, error)
}

// SpeechProvider generates speech.
type SpeechProvider interface {
	Speak(ctx context.Context, r SpeechRequest) (SpeechResult, error)
}

// ---------------------------------------------------------------- music ----

// MusicPrompt folds the structured flags into the single prompt string Lyria
// reads. Lyrics follow the documented "Lyrics:" header convention.
func MusicPrompt(r MusicRequest) (string, error) {
	prompt := strings.TrimSpace(r.Prompt)
	lyrics := strings.TrimSpace(r.Lyrics)
	if prompt == "" && lyrics == "" && len(r.Images) == 0 {
		return "", fmt.Errorf("provider.MusicPrompt: a prompt, lyrics or an image is required")
	}
	if r.Instrumental && lyrics != "" {
		return "", fmt.Errorf("provider.MusicPrompt: --instrumental and --lyrics are mutually exclusive")
	}
	if r.DurationSecs != 0 && r.Model.FixedSeconds > 0 {
		return "", fmt.Errorf("provider.MusicPrompt: model %q always generates %d s; --duration is not supported", r.Model.APIID, r.Model.FixedSeconds)
	}
	if r.DurationSecs < 0 {
		return "", fmt.Errorf("provider.MusicPrompt: --duration must be positive")
	}
	var parts []string
	if prompt != "" {
		parts = append(parts, prompt)
	}
	if r.Instrumental {
		parts = append(parts, "Instrumental only, no vocals.")
	}
	if r.DurationSecs > 0 {
		parts = append(parts, fmt.Sprintf("The song should be about %d seconds long.", r.DurationSecs))
	}
	if lyrics != "" {
		parts = append(parts, "Lyrics:\n"+lyrics)
	}
	return strings.Join(parts, "\n\n"), nil
}

// BuildMusicRequest builds the generateContent body for Lyria (pure, testable).
func BuildMusicRequest(r MusicRequest) ([]byte, error) {
	prompt, err := MusicPrompt(r)
	if err != nil {
		return nil, err
	}
	if len(r.Images) > 0 {
		if r.Model.MaxRefImages == 0 {
			return nil, fmt.Errorf("provider.BuildMusicRequest: model %q does not take image input", r.Model.APIID)
		}
		if len(r.Images) > r.Model.MaxRefImages {
			return nil, fmt.Errorf("provider.BuildMusicRequest: model %q accepts at most %d images, got %d", r.Model.APIID, r.Model.MaxRefImages, len(r.Images))
		}
	}
	if err := registry.ValidateFormat(r.Model, r.Format); err != nil {
		return nil, fmt.Errorf("provider.BuildMusicRequest: %w", err)
	}

	var parts []geminiPart
	if prompt != "" {
		parts = append(parts, geminiPart{Text: prompt})
	}
	for i, img := range r.Images {
		name := "image.png"
		if i < len(r.ImageNames) && r.ImageNames[i] != "" {
			name = r.ImageNames[i]
		}
		parts = append(parts, geminiPart{InlineData: &geminiInlineData{
			MimeType: detectContentType(name),
			Data:     base64.StdEncoding.EncodeToString(img),
		}})
	}
	body := map[string]any{"contents": []geminiContent{{Parts: parts}}}
	if strings.EqualFold(r.Format, "wav") {
		body["generationConfig"] = map[string]any{
			"responseModalities": []string{"AUDIO", "TEXT"},
			"responseFormat":     map[string]any{"audio": map[string]any{"mimeType": "AUDIO_WAV"}},
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildMusicRequest: %w", err)
	}
	return b, nil
}

// ParseMusicResponse collects every audio part and the text parts (lyrics /
// structure). Parts are order-independent, per the docs.
func ParseMusicResponse(data []byte, r MusicRequest) (MusicResult, error) {
	resp, err := decodeAudioResponse(data)
	if err != nil {
		return MusicResult{}, fmt.Errorf("provider.ParseMusicResponse: %w", err)
	}
	var out MusicResult
	var texts []string
	for _, p := range resp.parts() {
		if p.InlineData != nil && p.InlineData.Data != "" {
			raw, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				return MusicResult{}, fmt.Errorf("provider.ParseMusicResponse: decode: %w", err)
			}
			mime := p.InlineData.MimeType
			if mime == "" {
				mime = "audio/mpeg"
			}
			a := Audio{Data: raw, MimeType: mime}
			if strings.Contains(strings.ToLower(mime), "wav") {
				a.Seconds = WAVSeconds(raw)
			}
			out.Tracks = append(out.Tracks, a)
			continue
		}
		if t := strings.TrimSpace(p.Text); t != "" {
			texts = append(texts, t)
		}
	}
	if len(out.Tracks) == 0 {
		hint := ""
		if len(texts) > 0 {
			hint = ": " + strings.Join(texts, " ")
		}
		return MusicResult{}, fmt.Errorf("provider.ParseMusicResponse: no audio in response%s%s", hint, resp.blockNote())
	}
	out.Text = strings.Join(texts, "\n\n")
	if strings.EqualFold(strings.TrimSpace(out.Text), "<instrumental>") {
		out.Text = ""
	}
	out.CostUSD = float64(len(out.Tracks)) * r.Model.PricePerSong
	out.CostSource = CostSourceRegistry
	return out, nil
}

// GenerateMusic calls generateContent on a Lyria model. The call is billed per
// song, so it goes through DoOnce: no 5xx retry (SPEC §13.3).
func (g *Gemini) GenerateMusic(ctx context.Context, r MusicRequest) (MusicResult, error) {
	body, err := BuildMusicRequest(r)
	if err != nil {
		return MusicResult{}, err
	}
	resp, _, err := g.HTTP.DoOnce(ctx, http.MethodPost, g.endpoint(r.Model.APIID), body, g.headers())
	if err != nil {
		return MusicResult{}, fmt.Errorf("provider.Gemini.GenerateMusic: %w%s", err, notRetriedNote(err, "song"))
	}
	return ParseMusicResponse(resp, r)
}

// --------------------------------------------------------------- speech ----

// GeminiUsage is the generateContent usageMetadata object.
type GeminiUsage struct {
	PromptTokenCount     int                `json:"promptTokenCount"`
	CandidatesTokenCount int                `json:"candidatesTokenCount"`
	TotalTokenCount      int                `json:"totalTokenCount"`
	PromptTokensDetails  []geminiModalCount `json:"promptTokensDetails,omitempty"`
	CandidatesDetails    []geminiModalCount `json:"candidatesTokensDetails,omitempty"`
}

type geminiModalCount struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

// SpeechCostFromUsage prices a TTS call from usageMetadata: text input tokens
// at price_text_in, audio output tokens at price_audio_out (per 1M). Without
// per-modality details the totals are used (all prompt tokens are text, all
// candidate tokens are audio — true for TTS). ok=false when usage is absent.
func SpeechCostFromUsage(m registry.Model, u *GeminiUsage) (float64, bool) {
	if u == nil || (u.PromptTokenCount == 0 && u.CandidatesTokenCount == 0) {
		return 0, false
	}
	textIn, audioOut := u.PromptTokenCount, u.CandidatesTokenCount
	if d := modalTokens(u.PromptTokensDetails, "TEXT"); d > 0 {
		textIn = d
	}
	if d := modalTokens(u.CandidatesDetails, "AUDIO"); d > 0 {
		audioOut = d
	}
	return (float64(textIn)*m.PriceTextIn + float64(audioOut)*m.PriceAudioOut) / 1_000_000, true
}

func modalTokens(list []geminiModalCount, modality string) int {
	n := 0
	for _, d := range list {
		if strings.EqualFold(d.Modality, modality) {
			n += d.TokenCount
		}
	}
	return n
}

type speechMeta struct {
	Speaker string `json:"speaker,omitempty"`
	Style   string `json:"style,omitempty"`
}

type speechPart struct {
	Text string      `json:"text"`
	Meta *speechMeta `json:"speech_metadata,omitempty"`
}

// BuildSpeechRequest builds the generateContent body for Gemini 3.8 TTS (pure,
// testable). Text stays a verbatim transcript; style travels in
// speech_metadata, never in the text (the 3.8 prompting contract).
func BuildSpeechRequest(r SpeechRequest) ([]byte, error) {
	if len(r.Turns) == 0 {
		return nil, fmt.Errorf("provider.BuildSpeechRequest: no text")
	}
	for _, t := range r.Turns {
		if strings.TrimSpace(t.Text) == "" {
			return nil, fmt.Errorf("provider.BuildSpeechRequest: empty text turn")
		}
	}
	multi := len(r.Speakers) > 0
	var speechCfg map[string]any
	if multi {
		if len(r.Speakers) < 2 {
			return nil, fmt.Errorf("provider.BuildSpeechRequest: multi-speaker needs 2 --speaker pairs (use --voice for one voice)")
		}
		if r.Model.MaxSpeakers > 0 && len(r.Speakers) > r.Model.MaxSpeakers {
			return nil, fmt.Errorf("provider.BuildSpeechRequest: model %q supports at most %d speakers, got %d", r.Model.APIID, r.Model.MaxSpeakers, len(r.Speakers))
		}
		known := map[string]bool{}
		cfgs := make([]map[string]any, 0, len(r.Speakers))
		for _, s := range r.Speakers {
			if s.Speaker == "" || s.Voice == "" {
				return nil, fmt.Errorf("provider.BuildSpeechRequest: speaker needs a name and a voice")
			}
			known[s.Speaker] = true
			cfgs = append(cfgs, map[string]any{
				"speaker":     s.Speaker,
				"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": s.Voice}},
			})
		}
		for _, t := range r.Turns {
			if !known[t.Speaker] {
				return nil, fmt.Errorf("provider.BuildSpeechRequest: turn speaker %q has no --speaker voice", t.Speaker)
			}
		}
		speechCfg = map[string]any{"multiSpeakerVoiceConfig": map[string]any{"speakerVoiceConfigs": cfgs}}
	} else {
		voice := r.Voice
		if voice == "" {
			voice = r.Model.DefaultVoice
		}
		if voice == "" {
			return nil, fmt.Errorf("provider.BuildSpeechRequest: no voice")
		}
		speechCfg = map[string]any{"voiceConfig": map[string]any{"voice": voice}}
	}

	parts := make([]speechPart, 0, len(r.Turns))
	for _, t := range r.Turns {
		p := speechPart{Text: t.Text}
		if multi || t.Style != "" {
			m := speechMeta{Style: t.Style}
			if multi {
				m.Speaker = t.Speaker
			}
			p.Meta = &m
		}
		parts = append(parts, p)
	}

	genCfg := map[string]any{
		"responseModalities": []string{"AUDIO"},
		"speechConfig":       speechCfg,
	}
	if rf := speechResponseFormat(r); rf != nil {
		genCfg["responseFormat"] = rf
	}
	body := map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": genCfg,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("provider.BuildSpeechRequest: %w", err)
	}
	return b, nil
}

// speechResponseFormat is nil (API default: complete WAV, 24 kHz) unless raw
// PCM is requested. sampleRate is deliberately never sent: live 2026-10-04 the
// API ignores it (16000 still came back 24 kHz, for AUDIO_WAV and AUDIO_L16).
func speechResponseFormat(r SpeechRequest) map[string]any {
	if !strings.EqualFold(r.Format, "pcm") {
		return nil
	}
	return map[string]any{"audio": map[string]any{"mimeType": "AUDIO_L16"}}
}

// ParseSpeechResponse extracts the audio part and normalises it to a complete
// WAV: a RIFF payload passes through, headerless PCM (audio/L16;rate=…) is
// wrapped with a proper header. Cost comes from usageMetadata when present,
// else from the measured duration at the registry's rates.
func ParseSpeechResponse(data []byte, r SpeechRequest) (SpeechResult, error) {
	resp, err := decodeAudioResponse(data)
	if err != nil {
		return SpeechResult{}, fmt.Errorf("provider.ParseSpeechResponse: %w", err)
	}
	var raw []byte
	var mime string
	for _, p := range resp.parts() {
		if p.InlineData != nil && p.InlineData.Data != "" {
			raw, err = base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				return SpeechResult{}, fmt.Errorf("provider.ParseSpeechResponse: decode: %w", err)
			}
			mime = p.InlineData.MimeType
			break
		}
	}
	if len(raw) == 0 {
		return SpeechResult{}, fmt.Errorf("provider.ParseSpeechResponse: no audio in response%s", resp.blockNote())
	}
	out := SpeechResult{Usage: resp.UsageMetadata}
	switch {
	case IsWAV(raw):
		out.Audio = Audio{Data: raw, MimeType: "audio/wav", Seconds: WAVSeconds(raw)}
	case isPCMMime(mime):
		rate, ch := ParsePCMMime(mime)
		if rate == 0 {
			rate = 24000
		}
		wav := WrapPCMAsWAV(raw, rate, ch, 16)
		out.Audio = Audio{Data: wav, MimeType: "audio/wav", Seconds: WAVSeconds(wav)}
	default:
		return SpeechResult{}, fmt.Errorf("provider.ParseSpeechResponse: unsupported audio mime %q (expected audio/wav or audio/L16)", mime)
	}
	if usd, ok := SpeechCostFromUsage(r.Model, resp.UsageMetadata); ok {
		out.CostUSD, out.CostSource = usd, CostSourceUsage
	} else {
		out.CostUSD = registry.EstimateSpeechCost(r.Model, EstimateTextTokens(r.Turns), out.Audio.Seconds)
		out.CostSource = CostSourceRegistry
	}
	return out, nil
}

// EstimateTextTokens is a coarse pre-call input-token estimate (~4 chars/token).
func EstimateTextTokens(turns []SpeechTurn) int {
	n := 0
	for _, t := range turns {
		n += (len([]rune(t.Text)) + 3) / 4
	}
	return n
}

// EstimateSpeechSeconds is a coarse pre-call output-length guess for the cost
// guard (~15 characters of speech per second).
func EstimateSpeechSeconds(turns []SpeechTurn) float64 {
	n := 0
	for _, t := range turns {
		n += len([]rune(t.Text))
	}
	return float64(n) / 15
}

// Speak calls generateContent on a TTS model (billed: DoOnce, no 5xx retry).
func (g *Gemini) Speak(ctx context.Context, r SpeechRequest) (SpeechResult, error) {
	body, err := BuildSpeechRequest(r)
	if err != nil {
		return SpeechResult{}, err
	}
	resp, _, err := g.HTTP.DoOnce(ctx, http.MethodPost, g.endpoint(r.Model.APIID), body, g.headers())
	if err != nil {
		return SpeechResult{}, fmt.Errorf("provider.Gemini.Speak: %w%s", err, notRetriedNote(err, "audio"))
	}
	return ParseSpeechResponse(resp, r)
}

// ---------------------------------------------------------------- shared ----

// notRetriedNote explains the missing retry after a failed billed call. A 4xx
// was rejected before any work happened, so only 5xx / transport errors carry
// the "may already be billed" warning.
func notRetriedNote(err error, what string) string {
	if strings.HasPrefix(err.Error(), "HTTP 4") {
		return ""
	}
	return fmt.Sprintf(" (not retried: the server may already have generated and billed the %s)", what)
}

func (g *Gemini) headers() map[string]string {
	return map[string]string{
		"x-goog-api-key": g.APIKey,
		"Content-Type":   "application/json",
	}
}

type audioResponse struct {
	Candidates []struct {
		Content struct {
			Parts []audioPart `json:"parts"`
		} `json:"content"`
		FinishReason  string `json:"finishReason"`
		FinishMessage string `json:"finishMessage"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *GeminiUsage `json:"usageMetadata"`
}

type audioPart struct {
	Text       string `json:"text"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
}

func decodeAudioResponse(data []byte) (audioResponse, error) {
	var resp audioResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, err
	}
	if len(resp.Candidates) == 0 {
		return resp, fmt.Errorf("no candidates%s", resp.blockNote())
	}
	return resp, nil
}

func (a audioResponse) parts() []audioPart {
	if len(a.Candidates) == 0 {
		return nil
	}
	return a.Candidates[0].Content.Parts
}

// blockNote explains an empty response (safety block / finish reason).
func (a audioResponse) blockNote() string {
	var notes []string
	if a.PromptFeedback != nil && a.PromptFeedback.BlockReason != "" {
		notes = append(notes, "blockReason="+a.PromptFeedback.BlockReason)
	}
	if len(a.Candidates) > 0 && a.Candidates[0].FinishReason != "" && a.Candidates[0].FinishReason != "STOP" {
		n := "finishReason=" + a.Candidates[0].FinishReason
		if m := a.Candidates[0].FinishMessage; m != "" {
			n += " (" + m + ")"
		}
		notes = append(notes, n)
	}
	if len(notes) == 0 {
		return ""
	}
	return " [" + strings.Join(notes, ", ") + "]"
}

// AudioExtForMime maps an audio mime type to a file extension without the dot.
func AudioExtForMime(mime string) string {
	m := strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	switch m {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/wave", "audio/x-wav":
		return "wav"
	case "audio/ogg":
		return "ogg"
	case "audio/flac":
		return "flac"
	case "audio/mp4", "audio/aac", "audio/x-m4a":
		return "m4a"
	}
	if i := strings.Index(m, "/"); i >= 0 && i+1 < len(m) {
		return strings.TrimPrefix(m[i+1:], "x-")
	}
	return "bin"
}
