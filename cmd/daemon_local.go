package cmd

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/daemon/local"
	"github.com/spf13/cobra"
)

type DaemonLocalCmd struct {
	*flags.GlobalFlags
}

func NewDaemonLocalCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DaemonLocalCmd{
		GlobalFlags: flags,
	}
	cobraCmd := &cobra.Command{
		Use:    "daemon-local",
		Short:  "Run the local daemon for desktop app communication",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	return cobraCmd
}

func (cmd *DaemonLocalCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d, err := local.New(devsyConfig)
	if err != nil {
		return err
	}

	return d.Run(ctx)
}
