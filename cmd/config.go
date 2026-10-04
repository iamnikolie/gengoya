package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage gengoya profile config",
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write provider keys and defaults for a profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		r := bufio.NewReader(os.Stdin)

		fmt.Print("Default provider [openai|gemini]: ")
		provider, _ := r.ReadString('\n')
		provider = strings.TrimSpace(provider)
		if provider == "" {
			provider = "openai"
		}
		if provider != "openai" && provider != "gemini" {
			return fmt.Errorf("default provider must be openai or gemini")
		}

		fmt.Print("OpenAI API key (blank = skip): ")
		openaiKey, _ := r.ReadString('\n')
		openaiKey = strings.TrimSpace(openaiKey)

		fmt.Print("Gemini API key (blank = skip): ")
		geminiKey, _ := r.ReadString('\n')
		geminiKey = strings.TrimSpace(geminiKey)

		fmt.Print("Default model alias (blank = registry default): ")
		model, _ := r.ReadString('\n')
		model = strings.TrimSpace(model)

		cfg := &config.Config{
			DefaultProvider: provider,
			DefaultModel:    model,
			Providers:       map[string]config.ProviderKeys{},
		}
		if openaiKey != "" {
			cfg.Providers["openai"] = config.ProviderKeys{APIKey: openaiKey}
		}
		if geminiKey != "" {
			cfg.Providers["gemini"] = config.ProviderKeys{APIKey: geminiKey}
		}

		if err := config.Save(cfg, profile); err != nil {
			return err
		}
		dir, _ := config.ProfileDir(profile)
		fmt.Fprintf(os.Stderr, "Saved to %s/config.yaml\n", dir)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show profile defaults and masked key state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(profile)
		if err != nil {
			return err
		}
		fmt.Printf("profile: %s\n", profile)
		fmt.Printf("default_provider: %s\n", cfg.DefaultProvider)
		if cfg.DefaultModel != "" {
			fmt.Printf("default_model: %s\n", cfg.DefaultModel)
		} else {
			fmt.Printf("default_model: (registry default)\n")
		}
		for _, p := range []string{"openai", "gemini"} {
			key := cfg.APIKey(p)
			if key == "" {
				fmt.Printf("%s: (no key)\n", p)
			} else {
				fmt.Printf("%s: %s\n", p, maskKey(key))
			}
		}
		return nil
	},
}

func maskKey(key string) string {
	if len(key) <= 4 {
		return "***"
	}
	prefix := key
	if len(prefix) > 3 {
		prefix = key[:3]
	}
	// Prefer sk-…last4 style for OpenAI-like keys.
	if strings.HasPrefix(key, "sk-") {
		return "sk-…" + key[len(key)-4:]
	}
	return prefix + "…" + key[len(key)-4:]
}

func init() {
	configCmd.AddCommand(configInitCmd, configShowCmd)
	rootCmd.AddCommand(configCmd)
}
