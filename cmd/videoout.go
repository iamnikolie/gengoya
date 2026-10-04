package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/iamnikolie/gengoya/internal/provider"
)

// videoArtifact is one written clip plus its optional poster contact sheet.
type videoArtifact struct {
	Path   string
	Poster string
	Bytes  int
	Mime   string
}

// jsonVideoResult is the --json stdout payload for `video` / `jobs fetch`.
type jsonVideoResult struct {
	Videos          []jsonVideo `json:"videos"`
	Model           string      `json:"model"`
	APIID           string      `json:"api_id"`
	Provider        string      `json:"provider"`
	Resolution      string      `json:"resolution,omitempty"`
	DurationSeconds string      `json:"duration_seconds,omitempty"`
	Aspect          string      `json:"aspect,omitempty"`
	Operation       string      `json:"operation,omitempty"`
	JobID           string      `json:"job_id,omitempty"`
	State           string      `json:"state,omitempty"`
	CostEstimateUSD float64     `json:"cost_estimate_usd"`
	CostSource      string      `json:"cost_source,omitempty"`
	ElapsedMS       int64       `json:"elapsed_ms"`
}

type jsonVideo struct {
	Path   string `json:"path"`
	Poster string `json:"poster,omitempty"`
	Bytes  int    `json:"bytes"`
	Mime   string `json:"mime,omitempty"`
}

// buildVideoFilenames mirrors buildFilenames for video payloads.
func buildVideoFilenames(prompt, name string, videos []provider.Video) []string {
	n := len(videos)
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	ext := func(v provider.Video) string { return "." + provider.VideoExtForMime(v.MimeType) }
	if name != "" {
		base := sanitizeFilename(name)
		if n == 1 {
			out[0] = base + ext(videos[0])
		} else {
			for i := 0; i < n; i++ {
				out[i] = fmt.Sprintf("%s-%d%s", base, i+1, ext(videos[i]))
			}
		}
		return out
	}
	slug := slugifyPrompt(prompt)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%s-%s%s", slug, hashSuffix(prompt, i), ext(videos[i]))
	}
	return out
}

// writeVideos writes clips to outDir and, when posterFrames > 0 and ffmpeg is
// present, a PNG contact sheet next to each clip. Poster failures are reported
// on stderr but never fail the run — the clip is the deliverable.
func writeVideos(ctx context.Context, outDir, prompt, name string, videos []provider.Video, posterFrames int, duration float64, errW io.Writer) ([]videoArtifact, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("writeVideos: mkdir: %w", err)
	}
	used := map[string]bool{}
	if err := seedUsedFromDir(outDir, used); err != nil {
		return nil, fmt.Errorf("writeVideos: %w", err)
	}
	names := buildVideoFilenames(prompt, name, videos)

	posterOK := posterFrames > 0
	if posterOK && ffmpegPath() == "" {
		fmt.Fprintln(errW, "note: ffmpeg not found on PATH — skipping poster frames (mp4 is not viewable by an agent)")
		posterOK = false
	}

	out := make([]videoArtifact, len(videos))
	for i, v := range videos {
		fname := dedupeName(names[i], used)
		p := filepath.Join(outDir, fname)
		if err := os.WriteFile(p, v.Data, 0644); err != nil {
			return nil, fmt.Errorf("writeVideos: %w", err)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("writeVideos: %w", err)
		}
		art := videoArtifact{Path: abs, Bytes: len(v.Data), Mime: v.MimeType}

		if posterOK {
			poster := posterPathFor(abs)
			if err := extractPoster(ctx, abs, poster, posterFrames, duration); err != nil {
				fmt.Fprintf(errW, "note: poster extraction failed: %v\n", err)
			} else {
				used[filepath.Base(poster)] = true
				art.Poster = poster
			}
		}
		out[i] = art
	}
	return out, nil
}

// emitVideoResult writes paths (or JSON) to stdout and a status line to stderr.
// stdout order per clip: the video path, then the poster path — the poster is
// the one an agent can Read.
func emitVideoResult(arts []videoArtifact, meta jsonVideoResult, jsonOut bool, outW, errW io.Writer) error {
	for range arts {
		fmt.Fprintf(errW, "model=%s id=%s res=%s dur=%ss aspect=%s %s ms=%d\n",
			meta.Model, meta.APIID, meta.Resolution, meta.DurationSeconds, meta.Aspect,
			costLabel(meta.CostEstimateUSD), meta.ElapsedMS)
	}

	if jsonOut {
		meta.Videos = make([]jsonVideo, len(arts))
		for i, a := range arts {
			meta.Videos[i] = jsonVideo{Path: a.Path, Poster: a.Poster, Bytes: a.Bytes, Mime: a.Mime}
		}
		enc := json.NewEncoder(outW)
		enc.SetEscapeHTML(false)
		return enc.Encode(meta)
	}

	for _, a := range arts {
		fmt.Fprintln(outW, a.Path)
		if a.Poster != "" {
			fmt.Fprintln(outW, a.Poster)
		}
	}
	return nil
}

// costLabel renders the estimated USD price of a clip.
func costLabel(usd float64) string {
	return fmt.Sprintf("cost≈$%.2f", usd)
}

func artifactPaths(arts []videoArtifact) []string {
	var out []string
	for _, a := range arts {
		out = append(out, a.Path)
		if a.Poster != "" {
			out = append(out, a.Poster)
		}
	}
	return out
}
