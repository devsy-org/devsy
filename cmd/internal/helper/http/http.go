package http

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewHTTPCmd returns a new command.
func NewHTTPCmd(flags *flags.GlobalFlags) *cobra.Command {
	httpCmd := &cobra.Command{
		Use:    "http",
		Short:  "Devsy HTTP Utility Commands",
		Hidden: true,
	}

	httpCmd.AddCommand(NewRequestCmd())
	return httpCmd
}
