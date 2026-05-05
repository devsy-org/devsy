package templates

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewTemplatesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	templatesCmd := &cobra.Command{
		Use:           "templates",
		Short:         "Devcontainer template commands",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	templatesCmd.AddCommand(NewApplyCmd(globalFlags))
	templatesCmd.AddCommand(NewPublishCmd(globalFlags))
	templatesCmd.AddCommand(NewMetadataCmd(globalFlags))
	templatesCmd.AddCommand(NewGenerateDocsCmd(globalFlags))

	return templatesCmd
}
