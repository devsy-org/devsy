package daemon

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// NewCmd creates a new cobra command.
func NewCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	c := &cobra.Command{
		Use:    "daemon",
		Short:  "Devsy Pro Provider daemon commands",
		Args:   cobra.NoArgs,
		Hidden: true,
	}

	c.AddCommand(NewStartCmd(globalFlags))
	c.AddCommand(NewStatusCmd(globalFlags))
	c.AddCommand(NewNetcheckCmd(globalFlags))

	return c
}

func findProProvider(
	ctx context.Context,
	context, provider, host string,
) (*config.Config, *providerpkg.ProviderConfig, error) {
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, nil, err
	}

	pCfg, err := workspace.ProviderFromHost(ctx, devsyConfig, host)
	if err != nil {
		return devsyConfig, nil, fmt.Errorf("load provider: %w", err)
	}

	return devsyConfig, pCfg, nil
}
