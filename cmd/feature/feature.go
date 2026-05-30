package feature

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewFeatureCmd builds the 'devsy feature' parent command for inspecting and managing devcontainer features.
func NewFeatureCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "feature",
		Short: "Inspect and manage devcontainer features",
	}
}
