package provider

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewProviderCmd returns a new root command.
func NewProviderCmd(flags *flags.GlobalFlags) *cobra.Command {
	providerCmd := &cobra.Command{
		Use:   "provider",
		Short: "Devsy Provider commands",
	}

	providerCmd.AddCommand(NewAddCmd(flags))
	providerCmd.AddCommand(NewConfigureCmd(flags))
	providerCmd.AddCommand(NewDeleteCmd(flags))
	providerCmd.AddCommand(NewListCmd(flags))
	providerCmd.AddCommand(NewListAvailableCmd(flags))
	providerCmd.AddCommand(NewOptionsCmd(flags))
	providerCmd.AddCommand(NewRenameCmd(flags))
	providerCmd.AddCommand(NewSetOptionsCmd(flags))
	providerCmd.AddCommand(NewUpdateCmd(flags))
	providerCmd.AddCommand(NewUseCmd(flags))
	return providerCmd
}
