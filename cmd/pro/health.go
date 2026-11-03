package pro

import (
	"context"

	"github.com/devsy-org/devsy/cmd/pro/cmdutil"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/proxycmd"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/spf13/cobra"
)

// HealthCmd holds the cmd flags.
type HealthCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewHealthCmd creates a new command.
func NewHealthCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &HealthCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:              "health",
		Short:            "Check platform health",
		Hidden:           true,
		Annotations:      telemetry.SkipInUIAnnotation(),
		RunE:             cmdutil.HostRunE(globalFlags, &cmd.Host, cmd.Run),
		PersistentPreRun: cmdutil.MarkAgentExecuted,
	}
	cmdutil.AddHostFlag(c, &cmd.Host)

	return c
}

func (cmd *HealthCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	return proxycmd.RunAndPrint(ctx, proxycmd.Options{
		Command:     provider.Exec.Proxy.Health,
		DevsyConfig: devsyConfig,
		Provider:    provider,
	})
}
