package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the gengoya version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("gengoya version %s\n", buildVersion())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
