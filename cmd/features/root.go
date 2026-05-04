package features

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewFeaturesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	featuresCmd := &cobra.Command{
		Use:           "features",
		Short:         "Commands for inspecting and managing dev container features",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	featuresCmd.AddCommand(NewInfoCmd(globalFlags))
	featuresCmd.AddCommand(NewResolveDepsCmd(globalFlags))
	featuresCmd.AddCommand(NewGenerateDocsCmd(globalFlags))

	return featuresCmd
}
