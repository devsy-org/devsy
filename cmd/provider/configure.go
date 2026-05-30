package provider

import (
	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// ConfigureCmd holds flags for the `provider configure` subcommand.
type ConfigureCmd struct {
	*flags.GlobalFlags
	Reconfigure   bool
	SingleMachine bool
	Options       []string
	SkipInit      bool
}

// NewConfigureCmd creates the cobra command for `provider configure`.
func NewConfigureCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &ConfigureCmd{GlobalFlags: f}
	configureCmd := &cobra.Command{
		Use:   "configure [name]",
		Short: "Re-run init and option resolution for an existing provider",
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
			return ConfigureProvider(cobraCmd.Context(), ProviderOptionsConfig{
				Provider:       p.Config,
				Context:        devsyConfig.DefaultContext,
				UserOptions:    cmd.Options,
				Reconfigure:    cmd.Reconfigure,
				SkipRequired:   false,
				SkipInit:       cmd.SkipInit,
				SkipSubOptions: false,
				SingleMachine:  &cmd.SingleMachine,
			})
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
	configureCmd.Flags().
		BoolVar(&cmd.Reconfigure, "reconfigure", false, "Force re-resolution of all options")
	configureCmd.Flags().
		BoolVar(&cmd.SingleMachine, "single-machine", false, "Use a single machine for all workspaces")
	configureCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")
	configureCmd.Flags().
		BoolVar(&cmd.SkipInit, "skip-init", false, "Skip provider init (testing only)")
	_ = configureCmd.Flags().MarkHidden("skip-init")
	return configureCmd
}
