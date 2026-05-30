package self

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewSelfCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	_ = globalFlags
	return &cobra.Command{
		Use:   "self",
		Short: "Manage the devsy CLI itself",
	}
}
