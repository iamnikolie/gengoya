package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

var (
	musicLyrics       string
	musicLyricsFile   string
	musicInstrumental bool
	musicDuration     int
	musicImages       []string
)

var musicCmd = &cobra.Command{
	Use:   "music <prompt>",
	Short: "Generate a song or 30 s clip with Lyria (Gemini)",
	Long: `Generate music with Lyria (Gemini key). Writes the audio file and prints its path;
when the model returns lyrics / song structure they are saved next to it as
<name>.txt and that path is printed on the following line.

Lyria has no duration, negative-prompt or seed parameter: --instrumental,
--lyrics and --duration are folded into the prompt text.`,
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{"kind": registry.KindMusic},
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := ""
		if len(args) == 1 {
			prompt = args[0]
		}
		if nImages != 1 {
			return fmt.Errorf("music generation returns one track per request; -n must be 1")
		}
		if !registry.SupportsOp(sel.Model, "music") {
			return fmt.Errorf("model %q does not support music", sel.Alias)
		}
		lyrics := musicLyrics
		if musicLyricsFile != "" {
			if lyrics != "" {
				return fmt.Errorf("--lyrics and --lyrics-file are mutually exclusive")
			}
			b, err := os.ReadFile(musicLyricsFile)
			if err != nil {
				return fmt.Errorf("read --lyrics-file %s: %w", musicLyricsFile, err)
			}
			lyrics = string(b)
		}
		format := ""
		if cmd.Flags().Changed("format") {
			format = strings.ToLower(formatFlag)
		}
		if format == "" {
			format = registry.DefaultFormat(sel.Model)
		}

		req := provider.MusicRequest{
			Model: sel.Model, Prompt: prompt, Lyrics: lyrics,
			Instrumental: musicInstrumental, DurationSecs: musicDuration, Format: format,
		}
		for _, p := range musicImages {
			b, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("read --image %s: %w", p, err)
			}
			req.Images = append(req.Images, b)
			req.ImageNames = append(req.ImageNames, p)
		}
		// Pure build first: a refused flag combination fails before the cost prompt.
		if _, err := provider.BuildMusicRequest(req); err != nil {
			return err
		}
		if musicDuration > 0 {
			fmt.Fprintln(stderr, "note: --duration is a prompt hint only; the API has no duration parameter, actual length may differ")
		}

		est := sel.Model.PricePerSong
		if err := confirmCostThreshold(est, audioCostThreshold, assumeYes, os.Stdout, os.Stdin, stderr); err != nil {
			return err
		}

		start := time.Now()
		res, err := audioProv.GenerateMusic(cmd.Context(), req)
		if err != nil {
			return err
		}
		elapsed := time.Since(start).Milliseconds()

		slugSource := prompt
		if slugSource == "" {
			slugSource = lyrics
		}
		files, err := writeAudio(outDir, slugSource, nameBase, res.Tracks, res.Text)
		if err != nil {
			return err
		}
		secs := make([]float64, len(res.Tracks))
		for i, t := range res.Tracks {
			secs[i] = t.Seconds
		}
		return emitAudioResult(files, secs, audioMeta{
			Model: sel.Alias, APIID: sel.Model.APIID, Provider: sel.Provider,
			CostUSD: res.CostUSD, CostSource: res.CostSource, ElapsedMS: elapsed,
		}, jsonOutput, os.Stdout, stderr)
	},
}

func init() {
	musicCmd.Flags().StringVar(&musicLyrics, "lyrics", "", "your own lyrics (use [Verse]/[Chorus] tags); sent under a 'Lyrics:' header")
	musicCmd.Flags().StringVar(&musicLyricsFile, "lyrics-file", "", "read lyrics from a file")
	musicCmd.Flags().BoolVar(&musicInstrumental, "instrumental", false, "append 'Instrumental only, no vocals.' to the prompt")
	musicCmd.Flags().IntVar(&musicDuration, "duration", 0, "target length in seconds (prompt hint; refused for the fixed 30 s clip model)")
	musicCmd.Flags().StringArrayVar(&musicImages, "image", nil, "input image for image-to-music (repeatable, up to 10)")
	rootCmd.AddCommand(musicCmd)
}
