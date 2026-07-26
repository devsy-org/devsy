package env

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

func NewEnvCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	envCmd := &cobra.Command{
		Use:   "env",
		Short: "Devsy managed environment variables",
		Long: `Manage named environment variables stored per context and injected
into workspaces. Values are stored in plaintext in the Devsy config directory;
use "devsy secret" for sensitive values.`,
	}
	envCmd.AddCommand(NewSetCmd(globalFlags))
	envCmd.AddCommand(NewListCmd(globalFlags))
	envCmd.AddCommand(NewGetCmd(globalFlags))
	envCmd.AddCommand(NewDeleteCmd(globalFlags))
	return envCmd
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
