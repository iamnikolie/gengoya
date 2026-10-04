package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate [prompt]",
	Short: "Generate image(s) from a text prompt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
		if nImages < 1 {
			return fmt.Errorf("-n must be >= 1")
		}
		if !registry.SupportsOp(sel.Model, "generate") {
			return fmt.Errorf("model %q does not support generate", sel.Alias)
		}
		switch formatFlag {
		case "png", "jpeg", "webp":
		default:
			return fmt.Errorf("--format must be png|jpeg|webp")
		}

		if sel.Provider == "gemini" {
			if note := provider.GeminiQualityNote(resolvedQuality); note != "" {
				fmt.Fprintln(stderr, note)
			}
			if note := provider.GeminiFormatNote(formatFlag); note != "" {
				fmt.Fprintln(stderr, note)
			}
		}
		if note := openaiExtrasNote(backgroundFlag, formatFlag); note != "" {
			fmt.Fprintln(stderr, note)
		}

		est := estimateCost(nImages, sel.Model.PricePerImg)
		if err := confirmCost(est, assumeYes, os.Stdout, os.Stdin, stderr); err != nil {
			return err
		}

		req := provider.GenRequest{
			Model:       sel.Model,
			Prompt:      prompt,
			N:           nImages,
			Size:        resolvedSize,
			Aspect:      resolvedAspect,
			Quality:     resolvedQuality,
			Format:      formatFlag,
			Compression: -1,
		}
		fillOpenAIExtras(&req)

		start := time.Now()
		result, err := prov.Generate(cmd.Context(), req)
		if err != nil {
			return err
		}
		elapsed := time.Since(start).Milliseconds()

		paths, err := writeImages(outDir, prompt, nameBase, formatFlag, result.Images)
		if err != nil {
			return err
		}
		return emitResult(paths, result.Images, result, sel.Alias, sel.Model.APIID, sel.Provider, resolvedQuality, elapsed, jsonOutput, os.Stdout, stderr)
	},
}

func init() {
	rootCmd.AddCommand(generateCmd)
}
