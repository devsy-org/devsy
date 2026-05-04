package features

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewFeaturesCmd(flags *flags.GlobalFlags) *cobra.Command {
	featuresCmd := &cobra.Command{
		Use:   "features",
		Short: "Devcontainer feature commands",
	}

	featuresCmd.AddCommand(NewListCollectionCmd(flags))
	return featuresCmd
}
