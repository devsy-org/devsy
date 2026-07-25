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
		Short: "Devsy Secrets commands",
		Long: `Manage named secrets stored locally and injected into workspaces.

Secret values are kept in the OS keyring (macOS Keychain, Windows Credential
Manager, or libsecret) when available, and in an age-encrypted file otherwise.
Only non-sensitive metadata is stored in the Devsy config directory.`,
	}

	secretsCmd.AddCommand(NewSetCmd(flags))
	secretsCmd.AddCommand(NewListCmd(flags))
	secretsCmd.AddCommand(NewGetCmd(flags))
	secretsCmd.AddCommand(NewDeleteCmd(flags))
	secretsCmd.AddCommand(NewAttachCmd(flags))
	secretsCmd.AddCommand(NewDetachCmd(flags))
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
