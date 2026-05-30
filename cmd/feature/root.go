package feature

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewFeatureCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	featureCmd := &cobra.Command{
		Use:           "feature",
		Short:         "Commands for inspecting and managing dev container features",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	featureCmd.AddCommand(NewInfoCmd(globalFlags))
	featureCmd.AddCommand(NewResolveDepsCmd(globalFlags))
	featureCmd.AddCommand(NewGenerateDocsCmd(globalFlags))
	featureCmd.AddCommand(NewTestCmd(globalFlags))
	featureCmd.AddCommand(NewPackageCmd(globalFlags))
	featureCmd.AddCommand(NewPublishCmd(globalFlags))
	featureCmd.AddCommand(NewOutdatedCmd(globalFlags))
	featureCmd.AddCommand(NewUpgradeCmd(globalFlags))

	return featureCmd
}
