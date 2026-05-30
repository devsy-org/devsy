package feature

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewFeatureCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	_ = globalFlags
	return &cobra.Command{
		Use:   "feature",
		Short: "Inspect and manage devcontainer features",
	}
}
