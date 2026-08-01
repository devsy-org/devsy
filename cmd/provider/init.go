package provider

import (
	"os"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// InitCmd holds flags for the `provider init` subcommand.
type InitCmd struct {
	*flags.GlobalFlags
	Reset         bool
	SingleMachine bool
	Options       []string
	SkipInit      bool
}

// NewInitCmd creates the cobra command for `provider init`.
func NewInitCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &InitCmd{GlobalFlags: f}
	initCmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Run or re-run init and option resolution for an existing provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			name, err := resolveProviderName(args, devsyConfig.Current().DefaultProvider)
			if err != nil {
				return err
			}
			p, err := workspace.FindProvider(devsyConfig, name)
			if err != nil {
				return err
			}
			reporter, err := newStatusReporter(cmd.ResultFormat, os.Stdout)
			if err != nil {
				return err
			}
			if err := ConfigureProvider(cobraCmd.Context(), ProviderOptionsConfig{
				Provider:           p.Config,
				ContextName:        devsyConfig.DefaultContext,
				UserOptions:        cmd.Options,
				DiscardPriorValues: cmd.Reset,
				SkipInit:           cmd.SkipInit,
				SingleMachine:      &cmd.SingleMachine,
				Reporter:           reporter,
			}); err != nil {
				return err
			}
			status.Leave(reporter, status.PhaseReady, name)
			return nil
		},
		ValidArgsFunction: func(
			rootCmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
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
	cliflags.Add(initCmd,
		cliflags.Bool(&cmd.Reset, names.Reset, false,
			"Discard previously stored option answers and re-prompt from scratch"),
		cliflags.Bool(&cmd.SingleMachine, names.SingleMachine, false,
			"Use a single machine for all workspaces"),
		cliflags.StringArray(&cmd.Options, names.Option, nil,
			"Provider option in the form KEY=VALUE").Shorthand("o"),
		cliflags.Bool(&cmd.SkipInit, names.SkipInit, false,
			"Skip provider init (testing only)").Hidden(),
	)
	return initCmd
}
