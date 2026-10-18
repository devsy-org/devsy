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

// SelfCmd holds the cmd flags.
type SelfCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewSelfCmd creates a new command.
func NewSelfCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &SelfCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "self",
		Short:  "Get self",
		Hidden: true,
		RunE:   proutil.HostRunE(globalFlags, &cmd.Host, cmd.Run),
	}
	proutil.AddHostFlag(c, &cmd.Host)

	return c
}

func (cmd *SelfCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	return proxycmd.RunAndPrint(ctx, proxycmd.Options{
		Command:     provider.Exec.Proxy.Get.Self,
		DevsyConfig: devsyConfig,
		Provider:    provider,
	})
}
