package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlugifyPrompt(t *testing.T) {
	assert.Equal(t, "a-red-cube-on-white", slugifyPrompt("A red cube on white!"))
	assert.Equal(t, "one-two-three-four-five-six", slugifyPrompt("one two three four five six seven eight"))
	assert.Equal(t, "image", slugifyPrompt("!!!"))
	ru := slugifyPrompt("Привет! Это проверка синтеза речи в gengoya.")
	assert.True(t, utf8.ValidString(ru), "slug must not split a multi-byte letter: %q", ru)
	assert.Equal(t, "привет-это-проверка-синтеза-речи-в", ru)
	long := strings.Repeat("word ", 20)
	s := slugifyPrompt(long)
	assert.LessOrEqual(t, len(s), 40)
}

func TestSanitizeAndDedupe(t *testing.T) {
	assert.Equal(t, "cat.png", sanitizeFilename("../../cat.png"))
	used := map[string]bool{}
	assert.Equal(t, "a.png", dedupeName("a.png", used))
	assert.Equal(t, "a (1).png", dedupeName("a.png", used))
	assert.Equal(t, "a (2).png", dedupeName("a.png", used))
}

func TestBuildFilenames(t *testing.T) {
	one := buildFilenames("a red cube", "foo", "png", []provider.Image{{MimeType: "image/png"}})
	require.Len(t, one, 1)
	assert.Equal(t, "foo.png", one[0])

	many := buildFilenames("a red cube", "foo", "png", []provider.Image{
		{MimeType: "image/png"}, {MimeType: "image/png"}, {MimeType: "image/png"},
	})
	assert.Equal(t, []string{"foo-1.png", "foo-2.png", "foo-3.png"}, many)

	auto := buildFilenames("Hello World!", "", "webp", []provider.Image{{MimeType: "image/webp"}})
	require.Len(t, auto, 1)
	assert.True(t, strings.HasPrefix(auto[0], "hello-world-"))
	assert.True(t, strings.HasSuffix(auto[0], ".webp"))

	jpeg := buildFilenames("x", "logo", "png", []provider.Image{{MimeType: "image/jpeg"}})
	assert.Equal(t, "logo.jpeg", jpeg[0])
}

func TestWriteImagesDedupe(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.png"), []byte("old"), 0644))

	paths, err := writeImages(dir, "x", "foo", "png", []provider.Image{{Data: []byte("new"), Size: "1x1"}})
	require.NoError(t, err)
	require.Len(t, paths, 1)
	assert.True(t, strings.HasSuffix(paths[0], "foo (1).png"))
}

func TestEmitResultJSON(t *testing.T) {
	var out, errB bytes.Buffer
	imgs := []provider.Image{{Data: []byte("abc"), Size: "1024x1024"}}
	res := provider.GenResult{Images: imgs, CostUSD: 0.05, RevisedPrompt: "rev"}
	err := emitResult([]string{"/tmp/a.png"}, imgs, res, "gpt-image-2", "gpt-image-2", "openai", "high", 100, true, &out, &errB)
	require.NoError(t, err)

	var jr jsonResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &jr))
	assert.Equal(t, "/tmp/a.png", jr.Images[0].Path)
	assert.Equal(t, 3, jr.Images[0].Bytes)
	assert.Equal(t, 0.05, jr.CostEstimateUSD)
	assert.Contains(t, errB.String(), "cost≈$0.05")
	assert.Contains(t, errB.String(), `revised_prompt="rev"`)
}

func TestConfirmCostNonTTY(t *testing.T) {
	// os.Stdout may or may not be a TTY in CI; force non-prompt path via yes / low cost.
	require.NoError(t, confirmCost(0.10, false, nil, strings.NewReader(""), &bytes.Buffer{}))
	require.NoError(t, confirmCost(1.00, true, nil, strings.NewReader(""), &bytes.Buffer{}))
	// nil stdout treated as non-TTY → proceed
	require.NoError(t, confirmCost(1.00, false, nil, strings.NewReader("n\n"), &bytes.Buffer{}))
}

func TestEstimateCost(t *testing.T) {
	assert.InDelta(t, 0.15, estimateCost(3, 0.05), 1e-9)
}
