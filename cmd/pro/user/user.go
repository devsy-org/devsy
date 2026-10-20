package user

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewUserCmd builds the 'devsy pro user' parent command for managing Platform users.
func NewUserCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage Devsy Platform users",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewResetPasswordCmd(globalFlags))
	return cmd
}
