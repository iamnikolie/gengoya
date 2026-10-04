package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GENGOYA_HOME", dir)

	in := &Config{
		DefaultProvider: "openai",
		DefaultModel:    "gpt-image-2",
		Providers: map[string]ProviderKeys{
			"openai": {APIKey: "sk-test-openai"},
			"gemini": {APIKey: "AIza-test-gemini"},
		},
	}
	require.NoError(t, Save(in, "test"))

	path := filepath.Join(dir, "test", "config.yaml")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	out, err := Load("test")
	require.NoError(t, err)
	assert.Equal(t, "openai", out.DefaultProvider)
	assert.Equal(t, "gpt-image-2", out.DefaultModel)
	assert.Equal(t, "sk-test-openai", out.Providers["openai"].APIKey)
	assert.Equal(t, "AIza-test-gemini", out.Providers["gemini"].APIKey)
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("GENGOYA_HOME", t.TempDir())
	_, err := Load("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config init")
}

func TestAPIKeyEnvFallback(t *testing.T) {
	t.Setenv("GENGOYA_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "google-key")

	cfg := &Config{
		DefaultProvider: "openai",
		Providers:       map[string]ProviderKeys{},
	}
	assert.Equal(t, "sk-from-env", cfg.APIKey("openai"))
	assert.Equal(t, "google-key", cfg.APIKey("gemini"))

	cfg.Providers["openai"] = ProviderKeys{APIKey: "sk-from-file"}
	assert.Equal(t, "sk-from-file", cfg.APIKey("openai"))
}

func TestValidate(t *testing.T) {
	assert.Error(t, (&Config{}).Validate())
	assert.NoError(t, (&Config{DefaultProvider: "openai"}).Validate())
}

func TestProfileDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("GENGOYA_HOME", base)

	dir, err := ProfileDir("alice")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "alice"), dir)

	root, err := ProfileDir("")
	require.NoError(t, err)
	assert.Equal(t, base, root)
}
