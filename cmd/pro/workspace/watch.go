package workspace

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// WatchWorkspacesCmd holds the cmd flags.
type WatchWorkspacesCmd struct {
	*flags.GlobalFlags

	Host          string
	Project       string
	FilterByOwner bool
}

// NewWatchCmd creates a new command.
func NewWatchCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &WatchWorkspacesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "watch",
		Short:  "Watch workspaces",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := proutil.FindProProvider(
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

	cliflags.Add(c, cliflags.String(&cmd.Host, names.Host, "", "The pro instance to use"))
	_ = c.MarkFlagRequired(names.Host)
	flags.BindEnv(c.Flags(), names.Host)
	cliflags.Add(c, cliflags.String(&cmd.Project, names.Project, "", "The project to use"))
	_ = c.MarkFlagRequired(names.Project)
	flags.BindEnv(c.Flags(), names.Project)
	cliflags.Add(
		c,
		cliflags.Bool(
			&cmd.FilterByOwner,
			names.FilterByOwner,
			true,
			"If true only shows workspaces of current owner",
		),
	)

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
		Command: providerConfig.Exec.Proxy.Watch.Workspaces,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  providerConfig,
		Stdout:  os.Stdout,
		Stderr:  log.Writer(log.LevelError),
	})
	if err != nil {
		return fmt.Errorf("watch workspaces with provider %q: %w", providerConfig.Name, err)
	}

	return nil
}
