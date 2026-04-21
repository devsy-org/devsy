package pro

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	devsylog "github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// WatchWorkspacesCmd holds the cmd flags.
type WatchWorkspacesCmd struct {
	*flags.GlobalFlags

	Host          string
	Project       string
	FilterByOwner bool
}

// NewWatchWorkspacesCmd creates a new command.
func NewWatchWorkspacesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &WatchWorkspacesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "watch-workspaces",
		Short:  "Watch workspaces",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := findProProvider(
				cobraCmd.Context(),
				cmd.Context,
				cmd.Provider,
				cmd.Host,
			)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), devsyConfig, provider)
		},
	}

	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")
	c.Flags().StringVar(&cmd.Project, "project", "", "The project to use")
	_ = c.MarkFlagRequired("project")
	c.Flags().
		BoolVar(&cmd.FilterByOwner, "filter-by-owner", true, "If true only shows workspaces of current owner")

	return c
}

func (cmd *WatchWorkspacesCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	providerConfig *provider.ProviderConfig,
) error {
	opts := devsyConfig.ProviderOptions(providerConfig.Name)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if cmd.FilterByOwner {
		opts[config.EnvLoftFilterByOwner] = config.OptionValue{Value: "true"}
	}
	opts[config.EnvLoftProject] = config.OptionValue{Value: cmd.Project}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT)

	go func() {
		<-sigChan
		cancel()
	}()

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     cancelCtx,
		Name:    "watchWorkspaces",
		Command: providerConfig.Exec.Proxy.Watch.Workspaces,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  providerConfig,
		Stdout:  os.Stdout,
		Stderr:  devsylog.Writer(devsylog.LevelError),
	})
	if err != nil {
		return fmt.Errorf("watch workspaces with provider \"%s\": %w", providerConfig.Name, err)
	}

	return nil
}
