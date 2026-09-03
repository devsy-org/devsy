package secrets

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

func NewSecretsCmd(flags *flags.GlobalFlags) *cobra.Command {
	secretsCmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage secrets and external secret sources",
		Long: `Manage secrets and external secret sources used by Devsy workspaces.

Devsy-managed secret values are kept in the OS keyring (macOS Keychain,
Windows Credential Manager, or libsecret) when available, and in an
age-encrypted file otherwise. External sources such as SOPS remain owned by
their encrypted source file and are resolved only when needed.`,
	}

	secretsCmd.AddCommand(NewSetCmd(flags))
	secretsCmd.AddCommand(NewListCmd(flags))
	secretsCmd.AddCommand(NewGetCmd(flags))
	secretsCmd.AddCommand(NewDeleteCmd(flags))
	secretsCmd.AddCommand(NewAttachCmd(flags))
	secretsCmd.AddCommand(NewDetachCmd(flags))
	secretsCmd.AddCommand(NewSourceCmd(flags))
	return secretsCmd
}

func resolveContext(globalFlags *flags.GlobalFlags) (string, secrets.Store, error) {
	devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
	if err != nil {
		return "", nil, err
	}

	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return "", nil, err
	}

	return devsyConfig.DefaultContext, store, nil
}
