package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags

	IgnoreNotFound bool
	Force          bool
}

// NewDeleteCmd creates a new command.
func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: flags,
	}
	deleteCmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Deletes an existing provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetProviderSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
	}

	deleteCmd.Flags().
		BoolVar(&cmd.IgnoreNotFound, "ignore-not-found", false, "Treat \"provider not found\" as a successful delete")
	deleteCmd.Flags().
		BoolVar(&cmd.Force, "force", false, "Force delete the provider and ignore provider is already used")
	_ = deleteCmd.Flags().MarkHidden("force")
	return deleteCmd
}

func (cmd *DeleteCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	provider := devsyConfig.Current().DefaultProvider
	if len(args) > 0 {
		provider = args[0]
	} else if provider == "" {
		return fmt.Errorf("specify a provider to delete")
	}

	// delete the provider
	err = DeleteProvider(ctx, devsyConfig, provider, cmd.IgnoreNotFound, cmd.Force)
	if err != nil {
		return err
	}

	log.Infof("deleted provider: provider=%s", provider)
	return nil
}

func DeleteProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider string,
	ignoreNotFound, force bool,
) error {
	// if force is not set, check if the provider is associated with a pro instance or workspace
	if !force {
		// check if this provider is associated with a pro instance
		proInstances, err := workspace.ListProInstances(devsyConfig)
		if err != nil {
			return fmt.Errorf("list pro instances: %w", err)
		}
		for _, instance := range proInstances {
			if instance.Provider == provider {
				return fmt.Errorf(
					"cannot delete provider %q, because it is connected to Pro instance %q. "+
						"Removing the Pro instance will automatically delete this provider",
					instance.Provider,
					instance.Host,
				)
			}
		}

		// check if there are workspaces that still use this provider
		workspaces, err := workspace.List(ctx, devsyConfig, true, platform.AllOwnerFilter)
		if err != nil {
			return err
		}

		// search for workspace that uses this machine
		for _, workspace := range workspaces {
			if workspace.Provider.Name == provider {
				return fmt.Errorf(
					"cannot delete provider %q, because workspace %q is still using it. "+
						"Delete the workspace %q before deleting the provider",
					workspace.Provider.Name,
					workspace.ID,
					workspace.ID,
				)
			}
		}
	}

	return DeleteProviderConfig(devsyConfig, provider, ignoreNotFound)
}

func DeleteProviderConfig(devsyConfig *config.Config, provider string, ignoreNotFound bool) error {
	if devsyConfig.Current().DefaultProvider == provider {
		devsyConfig.Current().DefaultProvider = ""
	}
	delete(devsyConfig.Current().Providers, provider)
	err := config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	providerDir, err := provider2.GetProviderDir(devsyConfig.DefaultContext, provider)
	if err != nil {
		return err
	}
	_, err = os.Stat(providerDir)
	if err != nil {
		if os.IsNotExist(err) {
			if ignoreNotFound {
				return nil
			}

			return fmt.Errorf("provider %q does not exist", provider)
		}

		return err
	}
	err = os.RemoveAll(providerDir)
	if err != nil {
		return fmt.Errorf("delete provider dir: %w", err)
	}

	return nil
}
