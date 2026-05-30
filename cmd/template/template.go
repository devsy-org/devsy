package template

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewTemplateCmd builds the 'devsy template' parent command for working with devcontainer templates.
func NewTemplateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "template",
		Short: "Work with devcontainer templates",
	}
}
