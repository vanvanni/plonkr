// Package cmd wires the plonk CLI subcommands, flag parsing, and key preflight.
package cmd

import (
	"io"

	"github.com/spf13/cobra"
)

// New constructs the root plonk Cobra command wired to the supplied streams.
func New(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "plonk",
		Short:         "Configure existing machines over SSH",
		Long:          "Plonkr configures existing LXC containers, VMs, and bare metal over OpenSSH.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newRunCmd(), newValidateCmd(), newVersionCmd())
	return root
}
