package provider

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UseCmd holds the use cmd flags.
type UseCmd struct {
	*flags.GlobalFlags

	Reconfigure   bool
	SingleMachine bool
	Options       []string

	// only for testing
	SkipInit bool
}

// NewUseCmd creates a new command.
func NewUseCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &UseCmd{
		GlobalFlags: flags,
	}
	useCmd := &cobra.Command{
		Use:   "use [name]",
		Short: "Configure an existing provider and set as default",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("please specify the provider to use")
			}

			return cmd.Run(cobraCmd.Context(), args[0])
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

	AddFlags(useCmd, cmd)
	return useCmd
}

func AddFlags(useCmd *cobra.Command, cmd *UseCmd) {
	useCmd.Flags().
		BoolVar(&cmd.SingleMachine, "single-machine", false, "If enabled will use a single machine for all workspaces")
	useCmd.Flags().
		BoolVar(&cmd.Reconfigure, "reconfigure", false, "If enabled will not merge existing provider config")
	useCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")

	useCmd.Flags().
		BoolVar(&cmd.SkipInit, "skip-init", false, "ONLY FOR TESTING: If true will skip init")
	_ = useCmd.Flags().MarkHidden("skip-init")
}

// Run runs the command logic.
func (cmd *UseCmd) Run(ctx context.Context, providerName string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providerWithOptions, err := workspace.FindProvider(devsyConfig, providerName)
	if err != nil {
		return err
	}

	// should reconfigure?
	shouldReconfigure := cmd.Reconfigure || len(cmd.Options) > 0 ||
		providerWithOptions.State == nil ||
		cmd.SingleMachine
	if shouldReconfigure {
		return ConfigureProvider(ctx, ProviderOptionsConfig{
			Provider:       providerWithOptions.Config,
			Context:        devsyConfig.DefaultContext,
			UserOptions:    cmd.Options,
			Reconfigure:    cmd.Reconfigure,
			SkipRequired:   false,
			SkipInit:       cmd.SkipInit,
			SkipSubOptions: false,
			SingleMachine:  &cmd.SingleMachine,
		})
	}

	log.Infof(
		"To reconfigure provider %s, run with '--reconfigure' to reconfigure the provider",
		providerWithOptions.Config.Name,
	)
	return nil
}
