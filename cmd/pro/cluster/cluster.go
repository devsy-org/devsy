package cluster

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewClusterCmd builds the 'devsy pro cluster' parent command for managing
// Pro clusters.
func NewClusterCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage Devsy Pro clusters",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewAddCmd(globalFlags))
	cmd.AddCommand(NewListCmd(globalFlags))
	return cmd
}
