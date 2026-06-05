package mcp

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewMCPCmd builds the 'devsy mcp' parent command.
func NewMCPCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run Devsy as a Model Context Protocol server",
	}
	cmd.AddCommand(NewServeCmd(globalFlags))
	return cmd
}
