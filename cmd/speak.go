package cmd

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

var (
	speakVoice    string
	speakStyle    string
	speakFile     string
	speakSpeakers []string
)

var speakCmd = &cobra.Command{
	Use:   "speak [text|-]",
	Short: "Text-to-speech with Gemini 3.8 TTS (single voice or two-speaker dialogue)",
	Long: `Speak text with Gemini TTS (Gemini key). Text comes from the argument, --file, or
stdin ("-" or no argument with piped stdin). Writes a WAV file and prints its path.

Single voice:    gengoya speak "Hello" --voice Puck --style "warm, slow"
Two speakers:    gengoya speak --file script.txt --speaker Joe=Puck --speaker Jane=Kore
                 (script lines: "Joe: text" or "Joe [whispering]: text"; other lines
                 continue the previous turn)

Text is read verbatim. Put sustained delivery in --style; point-in-time effects go
inline as <laugh>, <sigh>, <short pause>, <long pause>.`,
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{"kind": registry.KindSpeech},
	RunE: func(cmd *cobra.Command, args []string) error {
		if nImages != 1 {
			return fmt.Errorf("speech generation returns one clip per request; -n must be 1")
		}
		if !registry.SupportsOp(sel.Model, "speak") {
			return fmt.Errorf("model %q does not support speak", sel.Alias)
		}
		text, err := readSpeakText(args, speakFile, os.Stdin)
		if err != nil {
			return err
		}

		speakers, err := parseSpeakerFlags(speakSpeakers)
		if err != nil {
			return err
		}
		format := ""
		if cmd.Flags().Changed("format") {
			format = strings.ToLower(formatFlag)
		}
		switch format {
		case "", "wav", "pcm":
		default:
			return fmt.Errorf("--format must be wav for speak (output is always a 24 kHz mono WAV)")
		}

		req := provider.SpeechRequest{
			Model: sel.Model, Voice: speakVoice, Speakers: speakers,
			Format: format,
		}
		voiceLabel := speakVoice
		if len(speakers) > 0 {
			if speakVoice != "" {
				return fmt.Errorf("--voice and --speaker are mutually exclusive")
			}
			req.Turns, err = parseDialogue(text, speakers, speakStyle)
			if err != nil {
				return err
			}
			voiceLabel = ""
		} else {
			req.Turns = []provider.SpeechTurn{{Text: text, Style: speakStyle}}
			if voiceLabel == "" {
				voiceLabel = sel.Model.DefaultVoice
			}
		}
		if _, err := provider.BuildSpeechRequest(req); err != nil {
			return err
		}

		est := registry.EstimateSpeechCost(sel.Model, provider.EstimateTextTokens(req.Turns), provider.EstimateSpeechSeconds(req.Turns))
		if err := confirmCostThreshold(est, audioCostThreshold, assumeYes, os.Stdout, os.Stdin, stderr); err != nil {
			return err
		}

		start := time.Now()
		res, err := audioProv.Speak(cmd.Context(), req)
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 400") {
				return fmt.Errorf("%w\nhint: a bare 400 usually means an unknown --voice / --speaker voice name (voices are prebuilt names, Extended Voice Library ids, or voice_… ids)", err)
			}
			return err
		}
		elapsed := time.Since(start).Milliseconds()

		files, err := writeAudio(outDir, text, nameBase, []provider.Audio{res.Audio}, "")
		if err != nil {
			return err
		}
		meta := audioMeta{
			Model: sel.Alias, APIID: sel.Model.APIID, Provider: sel.Provider,
			CostUSD: res.CostUSD, CostSource: res.CostSource, ElapsedMS: elapsed,
			Voice: voiceLabel, Usage: res.Usage,
		}
		for _, s := range speakers {
			meta.Speakers = append(meta.Speakers, s.Speaker+"="+s.Voice)
		}
		return emitAudioResult(files, []float64{res.Audio.Seconds}, meta, jsonOutput, os.Stdout, stderr)
	},
}

// readSpeakText resolves the speech text: file, then stdin ("-" or piped with
// no argument), then the argument.
func readSpeakText(args []string, file string, stdin *os.File) (string, error) {
	var text string
	switch {
	case file != "":
		if len(args) > 0 {
			return "", fmt.Errorf("give the text as an argument or --file, not both")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read --file %s: %w", file, err)
		}
		text = string(b)
	case len(args) == 1 && args[0] != "-":
		text = args[0]
	case len(args) == 1 || (stdin != nil && !isTTY(stdin)):
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		text = string(b)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("text is required: pass it as an argument, --file, or on stdin")
	}
	return text, nil
}

// parseSpeakerFlags parses repeated --speaker Name=Voice values.
func parseSpeakerFlags(vals []string) ([]provider.SpeakerVoice, error) {
	var out []provider.SpeakerVoice
	seen := map[string]bool{}
	for _, v := range vals {
		name, voice, ok := strings.Cut(v, "=")
		name, voice = strings.TrimSpace(name), strings.TrimSpace(voice)
		if !ok || name == "" || voice == "" {
			return nil, fmt.Errorf("--speaker %q: expected Name=Voice", v)
		}
		if seen[name] {
			return nil, fmt.Errorf("--speaker %q given twice", name)
		}
		seen[name] = true
		out = append(out, provider.SpeakerVoice{Speaker: name, Voice: voice})
	}
	return out, nil
}

var turnRE = regexp.MustCompile(`^\s*([^:\[\]]+?)\s*(?:\[([^\]]*)\])?\s*:\s*(.*)$`)

// parseDialogue splits a script into turns. A line opens a new turn only when
// its "Name:" / "Name [style]:" prefix is a declared speaker; any other
// non-blank line continues the previous turn. defaultStyle fills turns that
// carry no [style] of their own.
func parseDialogue(text string, speakers []provider.SpeakerVoice, defaultStyle string) ([]provider.SpeechTurn, error) {
	known := map[string]bool{}
	for _, s := range speakers {
		known[s.Speaker] = true
	}
	var turns []provider.SpeechTurn
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := turnRE.FindStringSubmatch(line); m != nil && known[strings.TrimSpace(m[1])] {
			style := strings.TrimSpace(m[2])
			if style == "" {
				style = defaultStyle
			}
			turns = append(turns, provider.SpeechTurn{Speaker: strings.TrimSpace(m[1]), Text: strings.TrimSpace(m[3]), Style: style})
			continue
		}
		if len(turns) == 0 {
			return nil, fmt.Errorf("script must start with a speaker line (\"Name: text\"); declared speakers: %s", speakerNames(speakers))
		}
		last := &turns[len(turns)-1]
		last.Text = strings.TrimSpace(last.Text + " " + line)
	}
	if len(turns) == 0 {
		return nil, fmt.Errorf("no dialogue turns found")
	}
	return turns, nil
}

func speakerNames(s []provider.SpeakerVoice) string {
	n := make([]string, len(s))
	for i, v := range s {
		n[i] = v.Speaker
	}
	return strings.Join(n, ", ")
}

func init() {
	speakCmd.Flags().StringVar(&speakVoice, "voice", "", "voice: prebuilt name (Kore, Puck, …), Extended Voice Library id, or voice_… id (default per model)")
	speakCmd.Flags().StringVar(&speakStyle, "style", "", "turn-level delivery direction, e.g. \"warm, slow\" (speech_metadata.style)")
	speakCmd.Flags().StringVar(&speakFile, "file", "", "read the text (or dialogue script) from a file")
	speakCmd.Flags().StringArrayVar(&speakSpeakers, "speaker", nil, "multi-speaker: Name=Voice (repeat for each of the 2 speakers)")
	rootCmd.AddCommand(speakCmd)
}
