package pro

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/pro/cmdutil"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	providercmd "github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UpdateProviderCmd holds the cmd flags.
type UpdateProviderCmd struct {
	*flags.GlobalFlags

	Host     string
	Instance string
}

// NewUpdateProviderCmd creates a new command.
func NewUpdateProviderCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &UpdateProviderCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "update-provider [new-version]",
		Short:  "Update platform provider",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	cmdutil.AddHostFlag(c, &cmd.Host)

	return c
}

func (cmd *UpdateProviderCmd) Run(ctx context.Context, args []string) error {
	newVersion, err := newVersionArg(args)
	if err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	provider, err := workspace.ProviderFromHost(ctx, devsyConfig, cmd.Host)
	if err != nil {
		return fmt.Errorf("load provider: %w", err)
	}
	if provider.Source.Internal {
		return nil
	}
	unlock, err := provider2.LockProvider(devsyConfig.DefaultContext, provider.Name)
	if err != nil {
		return err
	}
	defer unlock()
	devsyConfig, provider, err = cmd.reloadProvider(ctx, devsyConfig.DefaultContext)
	if err != nil || provider == nil {
		return err
	}

	return cmd.applyVersion(ctx, devsyConfig, provider, newVersion)
}

func newVersionArg(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("new version is missing")
	}

	return args[0], nil
}

func (cmd *UpdateProviderCmd) applyVersion(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider2.ProviderConfig,
	newVersion string,
) error {
	providerSource, err := resolveNewProviderSource(devsyConfig, provider.Name, newVersion)
	if err != nil {
		return err
	}

	providerName := provider.Name
	provider, err = workspace.UpdateProvider(ctx, devsyConfig, providerName, providerSource)
	if err != nil {
		return fmt.Errorf("update provider %s: %w", providerName, err)
	}

	// Automated version bump: re-resolve from the new schema's defaults
	// without prompting and without re-running init.
	err = providercmd.ConfigureProvider(ctx, providercmd.ProviderOptionsConfig{
		Provider:           provider,
		ContextName:        devsyConfig.DefaultContext,
		DiscardPriorValues: true,
		SkipRequired:       true,
		SkipInit:           true,
	})
	if err != nil {
		return fmt.Errorf(
			"configure provider, retry with 'devsy provider use %s --reconfigure': %w",
			provider.Name, err,
		)
	}

	return nil
}

// reloadProvider reads the config and provider again under the provider lock.
// A nil provider with a nil error means the provider is internal and needs no
// update.
func (cmd *UpdateProviderCmd) reloadProvider(
	ctx context.Context,
	contextName string,
) (*config.Config, *provider2.ProviderConfig, error) {
	devsyConfig, err := config.LoadConfig(contextName, cmd.Provider)
	if err != nil {
		return nil, nil, err
	}
	provider, err := workspace.ProviderFromHost(ctx, devsyConfig, cmd.Host)
	if err != nil {
		return nil, nil, fmt.Errorf("reload provider: %w", err)
	}
	if provider.Source.Internal {
		return nil, nil, nil
	}
	return devsyConfig, provider, nil
}

func resolveNewProviderSource(
	devsyConfig *config.Config,
	providerName, newVersion string,
) (string, error) {
	providerSource, err := workspace.ResolveProviderSource(
		devsyConfig,
		providerName,
	)
	if err != nil {
		return "", fmt.Errorf("resolve provider source %s: %w", providerName, err)
	}
	splitted := strings.Split(providerSource, "@")
	if len(splitted) == 0 {
		return "", fmt.Errorf("no provider source found %s", providerSource)
	}

	return splitted[0] + "@" + newVersion, nil
}
