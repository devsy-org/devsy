package user

import (
	platformflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewUserCmd builds the 'devsy platform user' parent command for managing Platform users.
func NewUserCmd(globalFlags *platformflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage Devsy Platform users",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewResetPasswordCmd(globalFlags))
	return cmd
}
