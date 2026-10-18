package pro

import (
	"context"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/client/proxycmd"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// VersionCmd holds the cmd flags.
type VersionCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewVersionCmd creates a new command.
func NewVersionCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &VersionCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "version",
		Short:  "Get version",
		Hidden: true,
		RunE:   proutil.HostRunE(globalFlags, &cmd.Host, cmd.Run),
	}
	proutil.AddHostFlag(c, &cmd.Host)

	return c
}

func (cmd *VersionCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	providerConfig *provider.ProviderConfig,
) error {
	return proxycmd.RunAndPrint(ctx, proxycmd.Options{
		Command:     providerConfig.Exec.Proxy.Get.Version,
		DevsyConfig: devsyConfig,
		Provider:    providerConfig,
		ExtraOptions: map[string]config.OptionValue{
			config.EnvProviderID:      {Value: providerConfig.Name},
			config.EnvProviderContext: {Value: cmd.Context},
		},
	})
}
