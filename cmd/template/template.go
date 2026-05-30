package template

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewTemplateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	templateCmd := &cobra.Command{
		Use:           "template",
		Short:         "Devcontainer template commands",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	templateCmd.AddCommand(NewApplyCmd(globalFlags))
	templateCmd.AddCommand(NewPublishCmd(globalFlags))
	templateCmd.AddCommand(NewMetadataCmd(globalFlags))
	templateCmd.AddCommand(NewGenerateDocsCmd(globalFlags))

	return templateCmd
}
