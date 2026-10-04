package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

var maskPath string

var editCmd = &cobra.Command{
	Use:   "edit [image...] [prompt]",
	Short: "Edit image(s) with a text prompt",
	Long: `Edit one or more input images. All leading args are image paths;
the last arg is the prompt. Optional --mask (PNG with alpha) for OpenAI;
for Gemini the mask is passed as an extra image part (no dedicated mask field).`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[len(args)-1]
		imagePaths := args[:len(args)-1]
		if nImages < 1 {
			return fmt.Errorf("-n must be >= 1")
		}
		if !registry.SupportsOp(sel.Model, "edit") {
			return fmt.Errorf("model %q does not support edit", sel.Alias)
		}
		switch formatFlag {
		case "png", "jpeg", "webp":
		default:
			return fmt.Errorf("--format must be png|jpeg|webp")
		}

		images := make([][]byte, len(imagePaths))
		for i, p := range imagePaths {
			b, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("read image %s: %w", p, err)
			}
			images[i] = b
		}
		var mask []byte
		if maskPath != "" {
			var err error
			mask, err = os.ReadFile(maskPath)
			if err != nil {
				return fmt.Errorf("read mask %s: %w", maskPath, err)
			}
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
			Images:      images,
			ImageNames:  imagePaths,
			Mask:        mask,
			Compression: -1,
		}
		fillOpenAIExtras(&req)

		start := time.Now()
		result, err := prov.Edit(cmd.Context(), req)
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
	editCmd.Flags().StringVar(&maskPath, "mask", "", "optional mask PNG (alpha) for OpenAI edits")
	rootCmd.AddCommand(editCmd)
}
