package strings

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewStringsCmd returns a new command.
func NewStringsCmd(flags *flags.GlobalFlags) *cobra.Command {
	stringsCmd := &cobra.Command{
		Use:    "strings",
		Short:  "Devsy String Utility Commands",
		Hidden: true,
	}

	return stringsCmd
}
