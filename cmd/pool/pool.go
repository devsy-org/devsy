package pool

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewPoolCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage pools of agent workspaces",
	}
	cmd.AddCommand(NewValidateCmd(globalFlags))
	cmd.AddCommand(NewAuditCmd())
	return cmd
}
