package template

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewTemplateCmd builds the 'devsy pro template' parent command for managing
// Pro templates.
func NewTemplateCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage Devsy Pro templates",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewListCmd(globalFlags))
	return cmd
}
