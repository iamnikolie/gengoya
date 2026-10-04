package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/iamnikolie/gengoya/internal/client"
	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

// version is the base CLI version; overridden via ldflags -X cmd.version=.
var version = "dev"

func buildVersion() string {
	v := version
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				rev := s.Value
				if len(rev) > 12 {
					rev = rev[:12]
				}
				v += " (" + rev + ")"
			}
		}
	}
	return v
}

var (
	profile         string
	providerFlag    string
	modelFlag       string
	outDir          string
	nameBase        string
	nImages         int
	sizeFlag        string
	aspectFlag      string
	qualityFlag     string
	formatFlag      string
	backgroundFlag  string
	compressionFlag int
	moderationFlag  string
	jsonOutput      bool
	verbose         bool
	assumeYes       bool

	cfg             *config.Config
	reg             *registry.Registry
	prov            provider.Provider
	videoProv       provider.VideoProvider
	sel             resolvedSelection
	resolvedSize    string
	resolvedAspect  string
	resolvedQuality string
	httpClient      *client.Client
)

var stderr io.Writer = os.Stderr

var rootCmd = &cobra.Command{
	Use:   "gengoya",
	Short: "Agent-facing image generation CLI (OpenAI + Gemini)",
	Long: `gengoya — agent-facing image generation from the terminal.

Writes image files to disk and prints their absolute paths to stdout.
Run 'gengoya skill' for the full agent reference.`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if profileExempt(cmd.Name()) {
			return nil
		}
		if profile == "" {
			return fmt.Errorf("--config <name> is required (or set GENGOYA_CONFIG); there is no default profile — run 'gengoya --config <name> config init'")
		}
		if cmd.Name() == "init" || cmd.Name() == "show" {
			return nil
		}

		var err error
		cfg, err = config.Load(profile)
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}

		profileDir, err := config.ProfileDir(profile)
		if err != nil {
			return err
		}
		reg, err = registry.Load(profileDir)
		if err != nil {
			return err
		}

		// models, and any command marked local, only need config + registry —
		// no provider, no API key.
		if cmd.Name() == "models" || cmd.Annotations["local"] == "true" {
			return nil
		}

		switch kind := cmd.Annotations["kind"]; kind {
		case "video":
			sel, err = resolveVideoModelProvider(reg, cfg, modelFlag, providerFlag)
		case registry.KindMusic, registry.KindSpeech:
			sel, err = resolveKindModelProvider(reg, cfg, kind, modelFlag, providerFlag)
		default:
			sel, err = resolveModelProvider(reg, cfg, modelFlag, providerFlag)
		}
		if err != nil {
			return err
		}

		key := cfg.APIKey(sel.Provider)
		if key == "" {
			return fmt.Errorf("no API key for provider %q: set providers.%s.api_key in config or the env var — run 'gengoya config init --config %s'", sel.Provider, sel.Provider, profile)
		}

		httpClient = client.New()
		httpClient.Verbose = verbose

		if k := cmd.Annotations["kind"]; k == registry.KindMusic || k == registry.KindSpeech {
			if sel.Provider != "gemini" {
				return fmt.Errorf("provider %q has no %s support", sel.Provider, k)
			}
			audioProv = provider.NewGemini(key, httpClient)
			return nil
		}

		if registry.IsVideo(sel.Model) {
			videoProv, err = videoProviderFor(sel.Provider)
			if err != nil {
				return err
			}
			return nil
		}
		if registry.IsMusic(sel.Model) || registry.IsSpeech(sel.Model) {
			return fmt.Errorf("model %q is a %s model; use 'gengoya %s'", sel.Alias, sel.Model.Kind, map[string]string{registry.KindMusic: "music", registry.KindSpeech: "speak"}[sel.Model.Kind])
		}

		resolvedSize, resolvedAspect, resolvedQuality, err = applySizeQualityAspect(sel.Model, sizeFlag, aspectFlag, qualityFlag)
		if err != nil {
			return err
		}
		if err := validateProviderExtras(sel.Provider, sel.Model, backgroundFlag, compressionFlag, moderationFlag, formatFlag); err != nil {
			return err
		}

		switch sel.Provider {
		case "openai":
			prov = provider.NewOpenAI(key, httpClient)
		case "gemini":
			prov = provider.NewGemini(key, httpClient)
		default:
			return fmt.Errorf("unsupported provider %q", sel.Provider)
		}
		return nil
	},
}

// videoProviderFor builds the video provider for a provider name, resolving its
// key from the profile. `jobs status/fetch` call it with the provider recorded
// in the job — which need not be the one the current flags would select.
func videoProviderFor(name string) (provider.VideoProvider, error) {
	if name != "gemini" {
		// Job records from before kie.ai was removed still name it.
		return nil, fmt.Errorf("provider %q has no video support in this version (kie.ai was removed); a job created with it cannot be polled or fetched here", name)
	}
	key := cfg.APIKey(name)
	if key == "" {
		return nil, fmt.Errorf("no API key for provider %q: set providers.%s.api_key in config or the env var — run 'gengoya config init --config %s'", name, name, profile)
	}
	if httpClient == nil {
		httpClient = client.New()
		httpClient.Verbose = verbose
	}
	return provider.NewGeminiVideo(key, httpClient), nil
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&profile, "config", os.Getenv("GENGOYA_CONFIG"), "profile to use (subdirectory of ~/.gengoya/)")
	rootCmd.PersistentFlags().StringVar(&providerFlag, "provider", "", "provider override: openai|gemini")
	rootCmd.PersistentFlags().StringVar(&modelFlag, "model", "", "registry model alias (infers provider)")
	rootCmd.PersistentFlags().StringVar(&outDir, "out", ".", "output directory")
	rootCmd.PersistentFlags().StringVar(&nameBase, "name", "", "output basename (no extension)")
	rootCmd.PersistentFlags().IntVarP(&nImages, "n", "n", 1, "number of images")
	rootCmd.PersistentFlags().StringVar(&sizeFlag, "size", "", "WxH, auto, or Gemini 1K/2K/4K/512 (model default if omitted)")
	rootCmd.PersistentFlags().StringVar(&aspectFlag, "aspect", "", "Gemini aspect ratio (e.g. 1:1, 16:9); ignored by OpenAI")
	rootCmd.PersistentFlags().StringVar(&qualityFlag, "quality", "", "low|medium|high|xhigh|max|auto (OpenAI; xhigh/max are gpt-image-2.5 only; ignored by Gemini)")
	rootCmd.PersistentFlags().StringVar(&formatFlag, "format", "png", "output format: png|jpeg|webp (OpenAI; Gemini uses response mime)")
	rootCmd.PersistentFlags().StringVar(&backgroundFlag, "background", "", "OpenAI: auto|transparent|opaque")
	rootCmd.PersistentFlags().IntVar(&compressionFlag, "compression", -1, "OpenAI jpeg/webp compression 0..100 (-1=omit)")
	rootCmd.PersistentFlags().StringVar(&moderationFlag, "moderation", "", "OpenAI: auto|low")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "JSON output on stdout")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "dump request/response (keys redacted) to stderr")
	rootCmd.PersistentFlags().BoolVar(&assumeYes, "yes", false, "confirm potentially costly batches without prompting")

	rootCmd.Version = buildVersion()
}

func profileExempt(name string) bool {
	switch name {
	case "gengoya", "skill", "help", "completion", "version", "bash", "zsh", "fish", "powershell":
		return true
	}
	return false
}
