package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
)

// audioCostThreshold is the TTY cost-guard threshold for music and speech.
// Both are far below it per call; the guard exists so a future price change or
// a huge --file does not bill silently on a terminal.
const audioCostThreshold = 0.50

var (
	// audioProv serves both `music` and `speak` (Gemini only for now).
	audioProv *provider.Gemini
)

// resolveKindModelProvider is resolveModelProvider for `music` / `speak`: it
// selects among models of one kind and falls back to the registry's
// music_defaults / speech_defaults.
func resolveKindModelProvider(reg *registry.Registry, cfg *config.Config, kind, modelFlag, providerFlag string) (resolvedSelection, error) {
	if modelFlag != "" {
		m, ok := reg.Lookup(modelFlag)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("unknown model alias %q — run 'gengoya models --kind %s --config <name>'", modelFlag, kind)
		}
		if m.Kind != kind {
			return resolvedSelection{}, fmt.Errorf("model %q is %s model, not %s; run 'gengoya models --kind %s'", modelFlag, withArticle(m.Kind), kind, kind)
		}
		if providerFlag != "" && providerFlag != m.Provider {
			return resolvedSelection{}, fmt.Errorf("--model %q belongs to provider %q, but --provider %q was set", modelFlag, m.Provider, providerFlag)
		}
		return resolvedSelection{Alias: modelFlag, Model: m, Provider: m.Provider}, nil
	}

	var candidates []string
	if providerFlag != "" {
		candidates = []string{providerFlag}
	} else {
		if cfg.DefaultProvider != "" {
			candidates = append(candidates, cfg.DefaultProvider)
		}
		candidates = append(candidates, reg.KindProviders(kind)...)
	}
	for _, p := range candidates {
		alias, ok := reg.DefaultKindFor(kind, p)
		if !ok {
			continue
		}
		m, ok := reg.Lookup(alias)
		if !ok {
			return resolvedSelection{}, fmt.Errorf("default %s model %q for provider %q missing from registry", kind, alias, p)
		}
		if m.Kind != kind {
			return resolvedSelection{}, fmt.Errorf("%s_defaults.%s points at %q, which is not a %s model", kind, p, alias, kind)
		}
		return resolvedSelection{Alias: alias, Model: m, Provider: p}, nil
	}
	if providerFlag != "" {
		return resolvedSelection{}, fmt.Errorf("provider %q has no %s model in the registry", providerFlag, kind)
	}
	return resolvedSelection{}, fmt.Errorf("no %s model in the registry — check %s_defaults", kind, kind)
}

func withArticle(kind string) string {
	if kind == registry.KindImage {
		return "an " + kind
	}
	return "a " + kind
}

// writtenAudio is one saved audio file (plus an optional text sidecar).
type writtenAudio struct {
	Path   string
	Text   string // sidecar path (lyrics), "" when none
	Bytes  int
	Mime   string
	Format string
}

// buildAudioFilenames mirrors buildFilenames for audio payloads; the slug comes
// from slugSource (prompt or speech text).
func buildAudioFilenames(slugSource, name string, audios []provider.Audio) []string {
	n := len(audios)
	out := make([]string, n)
	ext := func(a provider.Audio) string { return "." + provider.AudioExtForMime(a.MimeType) }
	if name != "" {
		base := sanitizeFilename(name)
		for i, a := range audios {
			if n == 1 {
				out[i] = base + ext(a)
			} else {
				out[i] = fmt.Sprintf("%s-%d%s", base, i+1, ext(a))
			}
		}
		return out
	}
	slug := slugifyPrompt(slugSource)
	if slug == "image" {
		slug = "audio"
	}
	for i, a := range audios {
		out[i] = fmt.Sprintf("%s-%s%s", slug, hashSuffix(slugSource, i), ext(a))
	}
	return out
}

// writeAudio writes audio files (and, for the first one, a "<base>.txt" text
// sidecar when text != "") into dir.
func writeAudio(dir, slugSource, name string, audios []provider.Audio, text string) ([]writtenAudio, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("writeAudio: mkdir: %w", err)
	}
	used := map[string]bool{}
	if err := seedUsedFromDir(dir, used); err != nil {
		return nil, fmt.Errorf("writeAudio: %w", err)
	}
	names := buildAudioFilenames(slugSource, name, audios)
	out := make([]writtenAudio, len(audios))
	for i, a := range audios {
		fname := dedupeName(names[i], used)
		p := filepath.Join(dir, fname)
		if err := os.WriteFile(p, a.Data, 0644); err != nil {
			return nil, fmt.Errorf("writeAudio: %w", err)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("writeAudio: %w", err)
		}
		w := writtenAudio{Path: abs, Bytes: len(a.Data), Mime: a.MimeType, Format: provider.AudioExtForMime(a.MimeType)}
		if i == 0 && text != "" {
			base := fname[:len(fname)-len(filepath.Ext(fname))]
			tp := filepath.Join(dir, dedupeName(base+".txt", used))
			if err := os.WriteFile(tp, []byte(text+"\n"), 0644); err != nil {
				return nil, fmt.Errorf("writeAudio: %w", err)
			}
			if w.Text, err = filepath.Abs(tp); err != nil {
				return nil, fmt.Errorf("writeAudio: %w", err)
			}
		}
		out[i] = w
	}
	return out, nil
}

// audioMeta is the common --json envelope for `music` and `speak`.
type audioMeta struct {
	Model           string
	APIID           string
	Provider        string
	CostUSD         float64
	CostSource      string
	ElapsedMS       int64
	Voice           string
	Speakers        []string
	DurationSeconds float64
	Usage           *provider.GeminiUsage
}

type jsonAudio struct {
	Path    string  `json:"path"`
	Lyrics  string  `json:"lyrics,omitempty"`
	Bytes   int     `json:"bytes"`
	Mime    string  `json:"mime,omitempty"`
	Seconds float64 `json:"duration_seconds,omitempty"`
}

type jsonAudioResult struct {
	Audio           []jsonAudio           `json:"audio"`
	Model           string                `json:"model"`
	APIID           string                `json:"api_id"`
	Provider        string                `json:"provider"`
	Voice           string                `json:"voice,omitempty"`
	Speakers        []string              `json:"speakers,omitempty"`
	CostEstimateUSD float64               `json:"cost_estimate_usd"`
	CostSource      string                `json:"cost_source,omitempty"`
	Usage           *provider.GeminiUsage `json:"usage,omitempty"`
	ElapsedMS       int64                 `json:"elapsed_ms"`
}

// emitAudioResult writes paths (or JSON) to stdout and one status line per file
// to stderr. stdout order per file: the audio path, then the text sidecar.
func emitAudioResult(files []writtenAudio, seconds []float64, meta audioMeta, jsonOut bool, outW, errW io.Writer) error {
	for i, f := range files {
		line := fmt.Sprintf("model=%s id=%s fmt=%s bytes=%d", meta.Model, meta.APIID, f.Format, f.Bytes)
		if i < len(seconds) && seconds[i] > 0 {
			line += fmt.Sprintf(" dur=%.1fs", seconds[i])
		}
		if meta.Voice != "" {
			line += " voice=" + meta.Voice
		}
		fmt.Fprintf(errW, "%s cost≈$%.4f (%s) ms=%d\n", line, meta.CostUSD, meta.CostSource, meta.ElapsedMS)
	}
	if jsonOut {
		jr := jsonAudioResult{
			Audio: make([]jsonAudio, len(files)), Model: meta.Model, APIID: meta.APIID, Provider: meta.Provider,
			Voice: meta.Voice, Speakers: meta.Speakers,
			CostEstimateUSD: meta.CostUSD, CostSource: meta.CostSource, Usage: meta.Usage, ElapsedMS: meta.ElapsedMS,
		}
		for i, f := range files {
			ja := jsonAudio{Path: f.Path, Lyrics: f.Text, Bytes: f.Bytes, Mime: f.Mime}
			if i < len(seconds) {
				ja.Seconds = seconds[i]
			}
			jr.Audio[i] = ja
		}
		enc := json.NewEncoder(outW)
		enc.SetEscapeHTML(false)
		return enc.Encode(jr)
	}
	for _, f := range files {
		fmt.Fprintln(outW, f.Path)
		if f.Text != "" {
			fmt.Fprintln(outW, f.Text)
		}
	}
	return nil
}
