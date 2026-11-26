package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version identifies the plonk CLI build.
var Version = "2026.11.0-dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the plonk CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "plonk %s\n", Version)
			return err
		},
	}
}
