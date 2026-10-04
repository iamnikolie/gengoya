package cmd

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Poster frames make a video readable by an agent: mp4 bytes cannot be viewed
// by Read, a PNG contact sheet can. Requires ffmpeg on PATH; without it the
// video is still written and a stderr note is printed.

func ffmpegPath() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

// probeDuration returns the video duration in seconds via ffprobe, or 0.
func probeDuration(ctx context.Context, videoPath string) float64 {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0
	}
	out, err := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath).Output()
	if err != nil {
		return 0
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// posterGrid picks a column/row layout for n frames.
func posterGrid(n int) (cols, rows int) {
	if n <= 1 {
		return 1, 1
	}
	cols = n
	if cols > 4 {
		cols = 4
	}
	rows = int(math.Ceil(float64(n) / float64(cols)))
	return cols, rows
}

// posterTimestamps returns n evenly spaced sample points inside a clip.
func posterTimestamps(duration float64, n int) []float64 {
	if n < 1 {
		return nil
	}
	if duration <= 0 {
		duration = 8
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = duration * (float64(i) + 0.5) / float64(n)
	}
	return out
}

// posterPathFor returns the poster path next to a video path.
func posterPathFor(videoPath string) string {
	ext := filepath.Ext(videoPath)
	return strings.TrimSuffix(videoPath, ext) + "-poster.png"
}

// extractPoster writes a PNG contact sheet of n frames sampled from videoPath.
// duration is the known clip length in seconds (0 → probe or assume 8).
func extractPoster(ctx context.Context, videoPath, posterPath string, frames int, duration float64) error {
	bin := ffmpegPath()
	if bin == "" {
		return fmt.Errorf("ffmpeg not found on PATH")
	}
	if frames < 1 {
		return fmt.Errorf("frames must be >= 1")
	}
	if duration <= 0 {
		duration = probeDuration(ctx, videoPath)
	}

	tmp, err := os.MkdirTemp("", "gengoya-poster-")
	if err != nil {
		return fmt.Errorf("extractPoster: %w", err)
	}
	defer os.RemoveAll(tmp)

	stamps := posterTimestamps(duration, frames)
	for i, ts := range stamps {
		frame := filepath.Join(tmp, fmt.Sprintf("f%02d.png", i+1))
		cmd := exec.CommandContext(ctx, bin,
			"-nostdin", "-y", "-loglevel", "error",
			"-ss", strconv.FormatFloat(ts, 'f', 3, 64),
			"-i", videoPath,
			"-frames:v", "1",
			"-update", "1",
			frame)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("extractPoster: ffmpeg frame %d: %w: %s", i+1, err, strings.TrimSpace(string(out)))
		}
	}

	if frames == 1 {
		src := filepath.Join(tmp, "f01.png")
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("extractPoster: %w", err)
		}
		if err := os.WriteFile(posterPath, data, 0644); err != nil {
			return fmt.Errorf("extractPoster: %w", err)
		}
		return nil
	}

	cols, rows := posterGrid(frames)
	cmd := exec.CommandContext(ctx, bin,
		"-nostdin", "-y", "-loglevel", "error",
		"-start_number", "1",
		"-i", filepath.Join(tmp, "f%02d.png"),
		"-vf", fmt.Sprintf("scale=480:-2,tile=%dx%d", cols, rows),
		"-frames:v", "1",
		"-update", "1",
		posterPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extractPoster: ffmpeg tile: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
