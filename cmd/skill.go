package cmd

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed skill.md
var skillDoc string

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Print the embedded agent skill reference",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print(skillDoc)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(skillCmd)
}
