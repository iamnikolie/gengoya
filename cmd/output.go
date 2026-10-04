package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/iamnikolie/gengoya/internal/provider"
)

// jsonResult is the --json stdout payload (SPEC §2).
type jsonResult struct {
	Images          []jsonImage     `json:"images"`
	Model           string          `json:"model"`
	APIID           string          `json:"api_id"`
	Provider        string          `json:"provider"`
	Quality         string          `json:"quality"`
	RevisedPrompt   string          `json:"revised_prompt"`
	CostEstimateUSD float64         `json:"cost_estimate_usd"`
	CostSource      string          `json:"cost_source,omitempty"`
	Usage           *provider.Usage `json:"usage,omitempty"`
	ElapsedMS       int64           `json:"elapsed_ms"`
}

type jsonImage struct {
	Path  string `json:"path"`
	Size  string `json:"size"`
	Bytes int    `json:"bytes"`
}

// slugifyPrompt builds a filename slug from the first ~6 words of prompt.
func slugifyPrompt(prompt string) string {
	fields := strings.Fields(prompt)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	var b strings.Builder
	for i, w := range fields {
		if i > 0 {
			b.WriteByte('-')
		}
		for _, r := range strings.ToLower(w) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			} else {
				b.WriteByte('-')
			}
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	// Cut by runes: a byte cut splits a Cyrillic letter and the OS refuses the
	// invalid-UTF-8 filename after the generation was already billed.
	if r := []rune(s); len(r) > 40 {
		s = strings.Trim(string(r[:40]), "-")
	}
	if s == "" {
		return "image"
	}
	return s
}

// sanitizeFilename strips path components and unsafe runes (from fibery).
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == 0 {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "file"
	}
	return out
}

// dedupeName returns name if unused, otherwise inserts " (n)" before the
// extension until unique. Records the chosen name in used.
func dedupeName(name string, used map[string]bool) string {
	if !used[name] {
		used[name] = true
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !used[cand] {
			used[cand] = true
			return cand
		}
	}
}

func hashSuffix(prompt string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", prompt, index, time.Now().UnixNano())))
	return fmt.Sprintf("%x", h[:4])
}

func extForFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return ".jpeg"
	case "webp":
		return ".webp"
	default:
		return ".png"
	}
}

func extForImage(img provider.Image, fallbackFormat string) string {
	if img.MimeType != "" {
		return "." + provider.ExtForMime(img.MimeType)
	}
	return extForFormat(fallbackFormat)
}

// buildFilenames returns n output basenames (with extension) for the run.
// When images is non-nil, each file's extension follows that image's mime type.
func buildFilenames(prompt, name, format string, images []provider.Image) []string {
	n := len(images)
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	if name != "" {
		base := sanitizeFilename(name)
		if n == 1 {
			out[0] = base + extForImage(images[0], format)
		} else {
			for i := 0; i < n; i++ {
				out[i] = fmt.Sprintf("%s-%d%s", base, i+1, extForImage(images[i], format))
			}
		}
		return out
	}
	slug := slugifyPrompt(prompt)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%s-%s%s", slug, hashSuffix(prompt, i), extForImage(images[i], format))
	}
	return out
}

// seedUsedFromDir marks existing files in dir so dedupe sees them.
func seedUsedFromDir(dir string, used map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			used[e.Name()] = true
		}
	}
	return nil
}

// estimateCost returns n * pricePerImg.
func estimateCost(n int, pricePerImg float64) float64 {
	return float64(n) * pricePerImg
}

// Cost-guard thresholds: video is billed per second, so a single default clip
// already costs more than the image threshold — it gets its own.
const (
	imageCostThreshold = 0.50
	videoCostThreshold = 1.00
)

// confirmCost prompts on TTY when estimate exceeds the image threshold.
func confirmCost(estimate float64, yes bool, stdout *os.File, stdin io.Reader, errW io.Writer) error {
	return confirmCostThreshold(estimate, imageCostThreshold, yes, stdout, stdin, errW)
}

// confirmCostThreshold prompts on TTY when estimate > threshold unless --yes.
// Non-TTY (agent) never prompts.
func confirmCostThreshold(estimate, threshold float64, yes bool, stdout *os.File, stdin io.Reader, errW io.Writer) error {
	if estimate <= threshold {
		return nil
	}
	if yes {
		return nil
	}
	if stdout == nil || !isTTY(stdout) {
		return nil
	}
	fmt.Fprintf(errW, "estimated cost ≈$%.2f — proceed? [y/N] ", estimate)
	r := bufio.NewReader(stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		return fmt.Errorf("aborted (estimated cost ≈$%.2f; pass --yes to confirm)", estimate)
	}
	return nil
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// writeImages writes image bytes to outDir and returns absolute paths.
func writeImages(outDir, prompt, name, format string, images []provider.Image) ([]string, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("writeImages: mkdir: %w", err)
	}
	used := map[string]bool{}
	if err := seedUsedFromDir(outDir, used); err != nil {
		return nil, fmt.Errorf("writeImages: %w", err)
	}
	names := buildFilenames(prompt, name, format, images)
	paths := make([]string, len(images))
	for i, img := range images {
		fname := dedupeName(names[i], used)
		path := filepath.Join(outDir, fname)
		if err := os.WriteFile(path, img.Data, 0644); err != nil {
			return nil, fmt.Errorf("writeImages: %w", err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("writeImages: %w", err)
		}
		paths[i] = abs
	}
	return paths, nil
}

// emitResult writes paths (or JSON) to stdout and status lines to stderr.
func emitResult(paths []string, images []provider.Image, result provider.GenResult, alias string, modelAPIID, providerName, quality string, elapsedMS int64, jsonOut bool, outW, errW io.Writer) error {
	size := ""
	if len(images) > 0 {
		size = images[0].Size
	}
	for i := range paths {
		sz := size
		if i < len(images) && images[i].Size != "" {
			sz = images[i].Size
		}
		fmt.Fprintf(errW, "model=%s id=%s size=%s quality=%s cost≈$%.2f ms=%d\n",
			alias, modelAPIID, sz, quality, result.CostUSD, elapsedMS)
	}
	if result.RevisedPrompt != "" {
		fmt.Fprintf(errW, "revised_prompt=%q\n", result.RevisedPrompt)
	}

	if jsonOut {
		jr := jsonResult{
			Images:          make([]jsonImage, len(paths)),
			Model:           alias,
			APIID:           modelAPIID,
			Provider:        providerName,
			Quality:         quality,
			RevisedPrompt:   result.RevisedPrompt,
			CostEstimateUSD: result.CostUSD,
			CostSource:      result.CostSource,
			Usage:           result.Usage,
			ElapsedMS:       elapsedMS,
		}
		for i, p := range paths {
			bytes := 0
			if i < len(images) {
				bytes = len(images[i].Data)
			}
			sz := size
			if i < len(images) && images[i].Size != "" {
				sz = images[i].Size
			}
			jr.Images[i] = jsonImage{Path: p, Size: sz, Bytes: bytes}
		}
		enc := json.NewEncoder(outW)
		enc.SetEscapeHTML(false)
		return enc.Encode(jr)
	}

	for _, p := range paths {
		fmt.Fprintln(outW, p)
	}
	return nil
}
