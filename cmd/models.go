package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

var modelsKind string

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Print the resolved model registry",
	RunE: func(cmd *cobra.Command, args []string) error {
		all := reg.All()
		switch modelsKind {
		case "":
		case registry.KindImage, registry.KindVideo, registry.KindMusic, registry.KindSpeech:
			for alias, m := range all {
				if m.Kind != modelsKind {
					delete(all, alias)
				}
			}
		default:
			return fmt.Errorf("--kind must be image, video, music or speech")
		}
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(all)
		}

		aliases := make([]string, 0, len(all))
		for a := range all {
			aliases = append(aliases, a)
		}
		sort.Strings(aliases)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ALIAS\tKIND\tPROVIDER\tAPI_ID\tOPS\tPRICE_NOTE")
		for _, a := range aliases {
			m := all[a]
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a, m.Kind, m.Provider, m.APIID, strings.Join(m.Ops, ","), m.PriceNote)
		}
		return w.Flush()
	},
}

func init() {
	modelsCmd.Flags().StringVar(&modelsKind, "kind", "", "filter by kind: image|video|music|speech")
	rootCmd.AddCommand(modelsCmd)
}
