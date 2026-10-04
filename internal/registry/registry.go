package registry

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var embeddedYAML []byte

// Size modes for ValidateSize.
const (
	SizeModeEnum     = "enum"     // strict sizes list (gpt-image-1*)
	SizeModeFlexible = "flexible" // gpt-image-2 constrained arbitrary WxH
	SizeModeGemini   = "gemini"   // WxH aliases + 1K/2K/4K tokens
)

// Model kinds. Empty kind means KindImage.
const (
	KindImage  = "image"
	KindVideo  = "video"
	KindMusic  = "music"
	KindSpeech = "speech"
)

// Model describes one registry alias.
type Model struct {
	Provider      string   `json:"provider"`        // "openai" | "gemini"
	Kind          string   `json:"kind"`            // "image" (default) | "video"
	APIID         string   `json:"api_id"`          // exact string sent to the provider
	Ops           []string `json:"ops"`             // "generate", "edit", "video"
	SizeMode      string   `json:"size_mode"`       // enum | flexible | gemini
	Sizes         []string `json:"sizes"`           // allowed --size values; first is default when omitted
	ImageSizes    []string `json:"image_sizes"`     // Gemini API imageSize caps (1K, 2K, …)
	AspectRatios  []string `json:"aspect_ratios"`   // Gemini API aspectRatio caps
	Qualities     []string `json:"qualities"`       // allowed --quality; empty = provider ignores quality
	PriceNote     string   `json:"price_note"`      // human hint shown by `gengoya models`
	PricePerImg   float64  `json:"price_per_img"`   // rough per-image USD fallback / pre-call estimate
	PriceTextIn   float64  `json:"price_text_in"`   // USD per 1M text input tokens (OpenAI usage)
	PriceImageIn  float64  `json:"price_image_in"`  // USD per 1M image input tokens
	PriceImageOut float64  `json:"price_image_out"` // USD per 1M image output tokens

	// Video-only fields (kind: video).
	Resolutions          []string           `json:"resolutions,omitempty"`            // allowed --resolution; first is the default
	Durations            []string           `json:"durations,omitempty"`              // allowed --duration (seconds); first is the default
	DurationByResolution map[string]string  `json:"duration_by_resolution,omitempty"` // resolution → the only duration it accepts
	RefRequiresDuration  string             `json:"ref_requires_duration,omitempty"`  // duration forced when --ref is used
	MaxRefImages         int                `json:"max_ref_images,omitempty"`         // cap on --ref count
	PricePerSec          map[string]float64 `json:"price_per_sec,omitempty"`          // resolution → USD per output second

	// Music-only fields (kind: music). Max reference images reuses MaxRefImages.
	PricePerSong float64  `json:"price_per_song,omitempty"` // flat USD per generated song/clip
	Formats      []string `json:"formats,omitempty"`        // allowed --format; first is the default
	FixedSeconds int      `json:"fixed_seconds,omitempty"`  // >0: output length is fixed (clip), --duration refused

	// Speech-only fields (kind: speech). Text input price reuses PriceTextIn.
	Voices            []string `json:"voices,omitempty"`               // prebuilt voice names (informational; extended-library ids also work)
	DefaultVoice      string   `json:"default_voice,omitempty"`        // voice used when --voice is omitted
	MaxSpeakers       int      `json:"max_speakers,omitempty"`         // cap on multi-speaker voices
	PriceAudioOut     float64  `json:"price_audio_out,omitempty"`      // USD per 1M audio output tokens
	AudioTokensPerSec float64  `json:"audio_tokens_per_sec,omitempty"` // audio output tokens per second of speech
}

type fileModel struct {
	Provider      string   `yaml:"provider"`
	Kind          string   `yaml:"kind"`
	APIID         string   `yaml:"api_id"`
	Ops           []string `yaml:"ops"`
	SizeMode      string   `yaml:"size_mode"`
	Sizes         []string `yaml:"sizes"`
	ImageSizes    []string `yaml:"image_sizes"`
	AspectRatios  []string `yaml:"aspect_ratios"`
	Qualities     []string `yaml:"qualities"`
	PriceNote     string   `yaml:"price_note"`
	PricePerImg   float64  `yaml:"price_per_img"`
	PriceTextIn   float64  `yaml:"price_text_in"`
	PriceImageIn  float64  `yaml:"price_image_in"`
	PriceImageOut float64  `yaml:"price_image_out"`

	Resolutions          []string           `yaml:"resolutions"`
	Durations            []string           `yaml:"durations"`
	DurationByResolution map[string]string  `yaml:"duration_by_resolution"`
	RefRequiresDuration  string             `yaml:"ref_requires_duration"`
	MaxRefImages         int                `yaml:"max_ref_images"`
	PricePerSec          map[string]float64 `yaml:"price_per_sec"`

	PricePerSong float64  `yaml:"price_per_song"`
	Formats      []string `yaml:"formats"`
	FixedSeconds int      `yaml:"fixed_seconds"`

	Voices            []string `yaml:"voices"`
	DefaultVoice      string   `yaml:"default_voice"`
	MaxSpeakers       int      `yaml:"max_speakers"`
	PriceAudioOut     float64  `yaml:"price_audio_out"`
	AudioTokensPerSec float64  `yaml:"audio_tokens_per_sec"`
}

type fileRegistry struct {
	Defaults       map[string]string    `yaml:"defaults"`
	VideoDefaults  map[string]string    `yaml:"video_defaults"`
	MusicDefaults  map[string]string    `yaml:"music_defaults"`
	SpeechDefaults map[string]string    `yaml:"speech_defaults"`
	Models         map[string]fileModel `yaml:"models"`
}

// Registry is a loaded model catalog.
type Registry struct {
	defaults      map[string]string
	videoDefaults map[string]string
	kindDefaults  map[string]map[string]string // music | speech → provider → alias
	models        map[string]Model
}

// Load returns the embedded registry, or a whole-file override from
// profileDir/registry.yaml when that file exists.
func Load(profileDir string) (*Registry, error) {
	data := embeddedYAML
	if profileDir != "" {
		override := filepath.Join(profileDir, "registry.yaml")
		if b, err := os.ReadFile(override); err == nil {
			data = b
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("registry.Load: %w", err)
		}
	}
	return Parse(data)
}

// Parse loads a registry from YAML bytes.
func Parse(data []byte) (*Registry, error) {
	var fr fileRegistry
	if err := yaml.Unmarshal(data, &fr); err != nil {
		return nil, fmt.Errorf("registry.Parse: %w", err)
	}
	if len(fr.Models) == 0 {
		return nil, fmt.Errorf("registry.Parse: no models")
	}
	r := &Registry{
		defaults:      fr.Defaults,
		videoDefaults: fr.VideoDefaults,
		kindDefaults: map[string]map[string]string{
			KindMusic:  fr.MusicDefaults,
			KindSpeech: fr.SpeechDefaults,
		},
		models: make(map[string]Model, len(fr.Models)),
	}
	if r.defaults == nil {
		r.defaults = map[string]string{}
	}
	if r.videoDefaults == nil {
		r.videoDefaults = map[string]string{}
	}
	for alias, m := range fr.Models {
		kind := m.Kind
		if kind == "" {
			kind = KindImage
		}
		mode := m.SizeMode
		if mode == "" {
			if m.Provider == "gemini" {
				mode = SizeModeGemini
			} else {
				mode = SizeModeEnum
			}
		}
		r.models[alias] = Model{
			Provider:      m.Provider,
			Kind:          kind,
			APIID:         m.APIID,
			Ops:           m.Ops,
			SizeMode:      mode,
			Sizes:         m.Sizes,
			ImageSizes:    m.ImageSizes,
			AspectRatios:  m.AspectRatios,
			Qualities:     m.Qualities,
			PriceNote:     m.PriceNote,
			PricePerImg:   m.PricePerImg,
			PriceTextIn:   m.PriceTextIn,
			PriceImageIn:  m.PriceImageIn,
			PriceImageOut: m.PriceImageOut,

			Resolutions:          m.Resolutions,
			Durations:            m.Durations,
			DurationByResolution: m.DurationByResolution,
			RefRequiresDuration:  m.RefRequiresDuration,
			MaxRefImages:         m.MaxRefImages,
			PricePerSec:          m.PricePerSec,

			PricePerSong: m.PricePerSong,
			Formats:      m.Formats,
			FixedSeconds: m.FixedSeconds,

			Voices:            m.Voices,
			DefaultVoice:      m.DefaultVoice,
			MaxSpeakers:       m.MaxSpeakers,
			PriceAudioOut:     m.PriceAudioOut,
			AudioTokensPerSec: m.AudioTokensPerSec,
		}
	}
	return r, nil
}

// Lookup returns the model for alias.
func (r *Registry) Lookup(alias string) (Model, bool) {
	m, ok := r.models[alias]
	return m, ok
}

// DefaultFor returns the default image alias for a provider.
func (r *Registry) DefaultFor(provider string) (string, bool) {
	alias, ok := r.defaults[provider]
	return alias, ok
}

// DefaultVideoFor returns the default video alias for a provider.
func (r *Registry) DefaultVideoFor(provider string) (string, bool) {
	alias, ok := r.videoDefaults[provider]
	return alias, ok
}

// DefaultKindFor returns the default alias for a music or speech provider.
func (r *Registry) DefaultKindFor(kind, provider string) (string, bool) {
	alias, ok := r.kindDefaults[kind][provider]
	return alias, ok
}

// KindProviders lists providers that declare a default for kind (music|speech), sorted.
func (r *Registry) KindProviders(kind string) []string {
	out := make([]string, 0, len(r.kindDefaults[kind]))
	for p := range r.kindDefaults[kind] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// VideoProviders lists providers that declare a default video model, sorted.
func (r *Registry) VideoProviders() []string {
	out := make([]string, 0, len(r.videoDefaults))
	for p := range r.videoDefaults {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// All returns a copy of the model map (alias → Model).
func (r *Registry) All() map[string]Model {
	out := make(map[string]Model, len(r.models))
	for k, v := range r.models {
		out[k] = v
	}
	return out
}

// AliasForAPIID finds the first alias whose APIID matches (for reverse lookup).
func (r *Registry) AliasForAPIID(apiID string) string {
	for alias, m := range r.models {
		if m.APIID == apiID {
			return alias
		}
	}
	return apiID
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// ValidateSize checks --size against the model's size_mode / allowed list.
func ValidateSize(m Model, size string) error {
	if size == "" {
		return nil
	}
	switch m.SizeMode {
	case SizeModeFlexible:
		if containsFold(m.Sizes, size) {
			return nil
		}
		if err := ValidateOpenAIFlexibleSize(size); err != nil {
			return fmt.Errorf("size %q not allowed for this model: %w; presets: %s", size, err, strings.Join(m.Sizes, ", "))
		}
		return nil
	case SizeModeGemini:
		if containsFold(m.Sizes, size) {
			return nil
		}
		return fmt.Errorf("size %q not allowed for this model; allowed: %s", size, strings.Join(m.Sizes, ", "))
	default: // enum
		if containsFold(m.Sizes, size) {
			return nil
		}
		return fmt.Errorf("size %q not allowed for this model; allowed: %s", size, strings.Join(m.Sizes, ", "))
	}
}

// ValidateOpenAIFlexibleSize enforces gpt-image-2 pixel constraints from OpenAI docs.
func ValidateOpenAIFlexibleSize(size string) error {
	if strings.EqualFold(size, "auto") {
		return nil
	}
	w, h, err := ParseWxH(size)
	if err != nil {
		return err
	}
	if w%16 != 0 || h%16 != 0 {
		return fmt.Errorf("edges must be multiples of 16")
	}
	if w > 3840 || h > 3840 {
		return fmt.Errorf("max edge is 3840px")
	}
	long, short := w, h
	if h > w {
		long, short = h, w
	}
	if short == 0 || long > short*3 {
		return fmt.Errorf("aspect ratio must be ≤ 3:1")
	}
	pixels := w * h
	if pixels < 655360 || pixels > 8294400 {
		return fmt.Errorf("total pixels must be in [655360, 8294400]")
	}
	return nil
}

// ParseWxH parses "1024x1536" into width and height.
func ParseWxH(size string) (w, h int, err error) {
	parts := strings.Split(strings.ToLower(size), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected WxH")
	}
	w, err = strconv.Atoi(parts[0])
	if err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("invalid width")
	}
	h, err = strconv.Atoi(parts[1])
	if err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("invalid height")
	}
	return w, h, nil
}

// ValidateAspect checks --aspect against the model's aspect_ratios. Empty
// aspect is always ok. Models that declare no aspect_ratios (OpenAI sizes the
// image via --size instead) reject a non-empty aspect.
func ValidateAspect(m Model, aspect string) error {
	if aspect == "" {
		return nil
	}
	if len(m.AspectRatios) == 0 {
		if m.Provider == "openai" {
			return fmt.Errorf("--aspect is not supported for OpenAI models; use --size WxH")
		}
		return fmt.Errorf("model %q does not declare aspect_ratios", m.APIID)
	}
	if containsFold(m.AspectRatios, aspect) {
		return nil
	}
	return fmt.Errorf("aspect %q not allowed for this model; allowed: %s", aspect, strings.Join(m.AspectRatios, ", "))
}

// AllowsImageSize reports whether Gemini imageSize token is in the model's caps.
func AllowsImageSize(m Model, imageSize string) bool {
	if imageSize == "" {
		return true
	}
	return containsFold(m.ImageSizes, imageSize)
}

// ValidateQuality checks quality against the model's allowed list.
func ValidateQuality(m Model, q string) error {
	if q == "" {
		return nil
	}
	if len(m.Qualities) == 0 {
		return nil
	}
	for _, allowed := range m.Qualities {
		if allowed == q {
			return nil
		}
	}
	return fmt.Errorf("quality %q not allowed for this model; allowed: %s", q, strings.Join(m.Qualities, ", "))
}

// SupportsOp reports whether the model lists op in Ops.
func SupportsOp(m Model, op string) bool {
	for _, o := range m.Ops {
		if o == op {
			return true
		}
	}
	return false
}

// DefaultSize returns the first size entry, or "auto".
func DefaultSize(m Model) string {
	if len(m.Sizes) > 0 {
		return m.Sizes[0]
	}
	return "auto"
}

// DefaultQuality returns the first quality entry, or "".
func DefaultQuality(m Model) string {
	if len(m.Qualities) > 0 {
		return m.Qualities[0]
	}
	return ""
}

// IsVideo reports whether the model produces video.
func IsVideo(m Model) bool {
	return m.Kind == KindVideo
}

// DefaultResolution returns the first declared resolution, or "".
func DefaultResolution(m Model) string {
	if len(m.Resolutions) > 0 {
		return m.Resolutions[0]
	}
	return ""
}

// DefaultDuration returns the first entry of the durations enum, or "".
func DefaultDuration(m Model) string {
	if len(m.Durations) > 0 {
		return m.Durations[0]
	}
	return ""
}

// ValidateResolution checks --resolution against the model's list.
func ValidateResolution(m Model, res string) error {
	if res == "" {
		return nil
	}
	if len(m.Resolutions) == 0 {
		return fmt.Errorf("model %q does not declare resolutions", m.APIID)
	}
	if containsFold(m.Resolutions, res) {
		return nil
	}
	return fmt.Errorf("resolution %q not allowed for this model; allowed: %s", res, strings.Join(m.Resolutions, ", "))
}

// ValidateDuration checks --duration against the model's enum.
func ValidateDuration(m Model, dur string) error {
	if dur == "" {
		return nil
	}
	if len(m.Durations) == 0 {
		return fmt.Errorf("model %q does not declare durations", m.APIID)
	}
	if containsFold(m.Durations, dur) {
		return nil
	}
	return fmt.Errorf("duration %q not allowed for this model; allowed: %s", dur, strings.Join(m.Durations, ", "))
}

// ValidateVideoCombo enforces registry-declared cross-field rules: some
// resolutions accept only one duration, and reference images may force one too.
func ValidateVideoCombo(m Model, res, dur string, refCount int) error {
	if m.MaxRefImages > 0 && refCount > m.MaxRefImages {
		return fmt.Errorf("model %q accepts at most %d reference image(s), got %d", m.APIID, m.MaxRefImages, refCount)
	}
	if refCount > 0 && m.MaxRefImages == 0 {
		return fmt.Errorf("model %q does not support reference images", m.APIID)
	}
	if want, ok := m.DurationByResolution[strings.ToLower(res)]; ok && dur != "" && dur != want {
		return fmt.Errorf("resolution %s requires --duration %s (got %s)", res, want, dur)
	}
	if refCount > 0 && m.RefRequiresDuration != "" && dur != "" && dur != m.RefRequiresDuration {
		return fmt.Errorf("reference images require --duration %s (got %s)", m.RefRequiresDuration, dur)
	}
	return nil
}

// ResolveVideoDuration returns the duration to send: the explicit one when set,
// otherwise the constraint-implied value, otherwise the model default.
func ResolveVideoDuration(m Model, res, dur string, refCount int) string {
	if dur != "" {
		return dur
	}
	if want, ok := m.DurationByResolution[strings.ToLower(res)]; ok {
		return want
	}
	if refCount > 0 && m.RefRequiresDuration != "" {
		return m.RefRequiresDuration
	}
	return DefaultDuration(m)
}

// VideoPricePerSec returns USD/second for a resolution. Unknown resolutions fall
// back to the model's most expensive declared rate (conservative estimate).
func VideoPricePerSec(m Model, res string) float64 {
	if len(m.PricePerSec) == 0 {
		return 0
	}
	if p, ok := m.PricePerSec[strings.ToLower(res)]; ok {
		return p
	}
	max := 0.0
	for _, p := range m.PricePerSec {
		if p > max {
			max = p
		}
	}
	return max
}

// EstimateVideoCost returns duration_seconds * price_per_sec(resolution).
func EstimateVideoCost(m Model, res, dur string) float64 {
	secs, err := strconv.ParseFloat(strings.TrimSpace(dur), 64)
	if err != nil || secs <= 0 {
		return 0
	}
	return secs * VideoPricePerSec(m, res)
}

// IsMusic reports whether the model produces music.
func IsMusic(m Model) bool { return m.Kind == KindMusic }

// IsSpeech reports whether the model produces speech.
func IsSpeech(m Model) bool { return m.Kind == KindSpeech }

// DefaultFormat returns the first declared output format, or "".
func DefaultFormat(m Model) string {
	if len(m.Formats) > 0 {
		return m.Formats[0]
	}
	return ""
}

// ValidateFormat checks a music --format against the model's list. Empty is ok.
func ValidateFormat(m Model, format string) error {
	if format == "" {
		return nil
	}
	if containsFold(m.Formats, format) {
		return nil
	}
	return fmt.Errorf("format %q not allowed for this model; allowed: %s", format, strings.Join(m.Formats, ", "))
}

// EstimateSpeechCost prices a TTS call from input text tokens and output audio
// seconds using the model's per-1M-token rates (audio = tokens/sec * seconds).
func EstimateSpeechCost(m Model, textTokens int, audioSeconds float64) float64 {
	tps := m.AudioTokensPerSec
	if tps <= 0 {
		tps = 25
	}
	return (float64(textTokens)*m.PriceTextIn + audioSeconds*tps*m.PriceAudioOut) / 1_000_000
}
