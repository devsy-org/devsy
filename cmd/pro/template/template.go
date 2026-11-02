package template

import (
	platformflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewTemplateCmd builds the 'devsy platform template' parent command for managing
// Platform templates.
func NewTemplateCmd(globalFlags *platformflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage Devsy Platform templates",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewListCmd(globalFlags))
	return cmd
}
