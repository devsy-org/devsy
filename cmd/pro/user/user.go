package user

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewUserCmd builds the 'devsy pro user' parent command for managing Pro users.
func NewUserCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage Devsy Pro users",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewResetPasswordCmd(globalFlags))
	return cmd
}
