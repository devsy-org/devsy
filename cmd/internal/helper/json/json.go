package json

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewJSONCmd returns a new command.
func NewJSONCmd(flags *flags.GlobalFlags) *cobra.Command {
	jsonCmd := &cobra.Command{
		Use:    "json",
		Short:  "Devsy JSON Utility Commands",
		Hidden: true,
	}

	jsonCmd.AddCommand(NewGetCmd())
	return jsonCmd
}
