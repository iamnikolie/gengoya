package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderKeys holds the API key for a single image provider.
type ProviderKeys struct {
	APIKey string `yaml:"api_key"`
}

// Config is the per-profile gengoya configuration.
type Config struct {
	DefaultProvider string                  `yaml:"default_provider"`
	DefaultModel    string                  `yaml:"default_model,omitempty"`
	Providers       map[string]ProviderKeys `yaml:"providers"`
}

// Validate errors when default_provider is unset.
func (c *Config) Validate() error {
	if c.DefaultProvider == "" {
		return fmt.Errorf("default_provider not set: run 'gengoya config init --config <name>'")
	}
	return nil
}

// APIKey returns the resolved API key for provider: config file first, then env.
func (c *Config) APIKey(provider string) string {
	if c.Providers != nil {
		if p, ok := c.Providers[provider]; ok && p.APIKey != "" {
			return p.APIKey
		}
	}
	switch provider {
	case "openai":
		return firstEnv("OPENAI_API_KEY")
	case "gemini":
		return firstEnv("GEMINI_API_KEY", "GOOGLE_API_KEY")
	}
	return ""
}

// firstEnv returns the first non-empty environment variable among names.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// ProfileDir returns the config directory for the given profile.
// Empty profile → ~/.gengoya (or GENGOYA_HOME for tests).
// Non-empty profile → ~/.gengoya/<profile>.
func ProfileDir(profile string) (string, error) {
	return gengoyaHome(profile)
}

// gengoyaHome returns the config directory for the given profile.
func gengoyaHome(profile string) (string, error) {
	var base string
	if h := os.Getenv("GENGOYA_HOME"); h != "" {
		base = h
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".gengoya")
	}
	if profile != "" {
		return filepath.Join(base, profile), nil
	}
	return base, nil
}

// Load reads config from the profile's directory.
func Load(profile string) (*Config, error) {
	dir, err := gengoyaHome(profile)
	if err != nil {
		return nil, fmt.Errorf("config.Load: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config.Load: no config at %s — run 'gengoya config init --config %s'", filepath.Join(dir, "config.yaml"), profile)
		}
		return nil, fmt.Errorf("config.Load: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config.Load: parse: %w", err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderKeys{}
	}
	return &cfg, nil
}

// Save writes config to the profile's directory (~/.gengoya/<profile>/config.yaml).
// Directory mode 0700, file mode 0600.
func Save(cfg *Config, profile string) error {
	dir, err := gengoyaHome(profile)
	if err != nil {
		return fmt.Errorf("config.Save: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config.Save: mkdir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config.Save: marshal: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
		return fmt.Errorf("config.Save: %w", err)
	}
	return nil
}
