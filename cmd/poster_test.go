package cmd

import (
	"bytes"
	"context"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleVideo renders a tiny clip with ffmpeg itself, so the poster tests need
// no fixture, no network and no API spend. Skips where ffmpeg is absent (CI).
func sampleVideo(t *testing.T, dir string, seconds int) string {
	t.Helper()
	bin := ffmpegPath()
	if bin == "" {
		t.Skip("ffmpeg not on PATH")
	}
	path := filepath.Join(dir, "sample.mp4")
	cmd := exec.Command(bin,
		"-nostdin", "-y", "-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration="+itoa(seconds)+":size=160x120:rate=10",
		"-pix_fmt", "yuv420p",
		path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot synthesize a test clip: %v: %s", err, out)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func decodePNGSize(t *testing.T, path string) (int, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	return cfg.Width, cfg.Height
}

func TestExtractPosterSingleFrame(t *testing.T) {
	dir := t.TempDir()
	vid := sampleVideo(t, dir, 2)
	poster := posterPathFor(vid)

	require.NoError(t, extractPoster(context.Background(), vid, poster, 1, 2))
	w, h := decodePNGSize(t, poster)
	// A single frame keeps the source resolution (no scale/tile pass).
	assert.Equal(t, 160, w)
	assert.Equal(t, 120, h)
}

func TestExtractPosterContactSheet(t *testing.T) {
	dir := t.TempDir()
	vid := sampleVideo(t, dir, 2)
	poster := posterPathFor(vid)

	require.NoError(t, extractPoster(context.Background(), vid, poster, 4, 2))
	w, h := decodePNGSize(t, poster)
	// 4 frames → 4x1 tile of 480-wide frames.
	assert.Equal(t, 480*4, w)
	assert.Greater(t, h, 0)
	assert.Less(t, h, w)
}

func TestExtractPosterRejectsZeroFrames(t *testing.T) {
	dir := t.TempDir()
	assert.Error(t, extractPoster(context.Background(), filepath.Join(dir, "x.mp4"), filepath.Join(dir, "p.png"), 0, 2))
}

func TestExtractPosterUnreadableVideo(t *testing.T) {
	if ffmpegPath() == "" {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "not-a-video.mp4")
	require.NoError(t, os.WriteFile(bad, []byte("garbage"), 0644))
	assert.Error(t, extractPoster(context.Background(), bad, filepath.Join(dir, "p.png"), 1, 2))
}

func TestProbeDuration(t *testing.T) {
	dir := t.TempDir()
	vid := sampleVideo(t, dir, 2)
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}
	assert.InDelta(t, 2.0, probeDuration(context.Background(), vid), 0.3)
	assert.Zero(t, probeDuration(context.Background(), filepath.Join(dir, "missing.mp4")))
}

// writeVideos must produce the clip even when posters are disabled or ffmpeg
// fails — the clip is the deliverable, the poster is a convenience.
func TestWriteVideosPosterDisabled(t *testing.T) {
	dir := t.TempDir()
	var errW bytes.Buffer
	arts, err := writeVideos(context.Background(), dir, "a prompt", "clip",
		[]provider.Video{{Data: []byte("fake mp4 bytes"), MimeType: "video/mp4"}}, 0, 4, &errW)
	require.NoError(t, err)
	require.Len(t, arts, 1)
	assert.Equal(t, filepath.Join(dir, "clip.mp4"), arts[0].Path)
	assert.Empty(t, arts[0].Poster)
	assert.FileExists(t, arts[0].Path)
}

func TestWriteVideosPosterFailureIsNonFatal(t *testing.T) {
	if ffmpegPath() == "" {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	var errW bytes.Buffer
	// Not a real video: ffmpeg will fail, the clip must still be written.
	arts, err := writeVideos(context.Background(), dir, "a prompt", "clip",
		[]provider.Video{{Data: []byte("garbage"), MimeType: "video/mp4"}}, 4, 4, &errW)
	require.NoError(t, err)
	require.Len(t, arts, 1)
	assert.FileExists(t, arts[0].Path)
	assert.Empty(t, arts[0].Poster)
	assert.Contains(t, errW.String(), "poster extraction failed")
}

func TestWriteVideosDedupesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("older"), 0644))

	var errW bytes.Buffer
	arts, err := writeVideos(context.Background(), dir, "a prompt", "clip",
		[]provider.Video{{Data: []byte("new"), MimeType: "video/mp4"}}, 0, 4, &errW)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "clip (1).mp4"), arts[0].Path)

	old, err := os.ReadFile(filepath.Join(dir, "clip.mp4"))
	require.NoError(t, err)
	assert.Equal(t, "older", string(old), "an existing clip must never be overwritten")
}
