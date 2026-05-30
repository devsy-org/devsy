package template

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewTemplateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	_ = globalFlags
	return &cobra.Command{
		Use:   "template",
		Short: "Work with devcontainer templates",
	}
}
