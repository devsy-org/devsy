package config

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewConfigCmd builds the 'devsy config' parent command for reading and
// applying devcontainer configuration.
func NewConfigCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and apply devcontainer configuration",
	}
	cmd.AddCommand(NewReadCmd(globalFlags))
	cmd.AddCommand(NewApplyCmd(globalFlags))
	return cmd
}
