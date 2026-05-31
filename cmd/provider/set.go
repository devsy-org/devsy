package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// SetCmd holds the set cmd flags.
type SetCmd struct {
	*flags.GlobalFlags

	Dry      bool
	SkipInit bool

	SingleMachine bool
	Options       []string
}

// NewSetCmd creates a new command.
func NewSetCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &SetCmd{
		GlobalFlags: f,
	}
	setCmd := &cobra.Command{
		Use:   "set [provider]",
		Short: "Set provider options",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
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

	setCmd.Flags().
		BoolVar(&cmd.SingleMachine, "single-machine", false, "If enabled will use a single machine for all workspaces")
	setCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")
	setCmd.Flags().
		BoolVar(&cmd.Dry, "dry", false, "Dry will not persist the options to file and instead return the new filled options")
	setCmd.Flags().
		BoolVar(&cmd.SkipInit, "skip-init", false, "If true will skip running the provider init command")
	return setCmd
}

func (cmd *SetCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, providerWithOptions, err := cmd.loadProvider(args)
	if err != nil {
		return err
	}

	devsyConfig, err = configureProviderOptions(ctx, ProviderOptionsConfig{
		Provider:      providerWithOptions.Config,
		ContextName:   devsyConfig.DefaultContext,
		UserOptions:   cmd.Options,
		SkipRequired:  cmd.Dry,
		SkipInit:      cmd.Dry || cmd.SkipInit,
		SingleMachine: &cmd.SingleMachine,
	})
	if err != nil {
		return err
	}

	if err := cmd.saveOrPrintConfig(devsyConfig, providerWithOptions); err != nil {
		return err
	}

	log.Infof("set options for provider: providerName=%s", providerWithOptions.Config.Name)
	return nil
}

func (cmd *SetCmd) loadProvider(
	args []string,
) (*config.Config, *workspace.ProviderWithOptions, error) {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return nil, nil, err
	}

	providerName, err := resolveProviderName(args, devsyConfig.Current().DefaultProvider)
	if err != nil {
		return nil, nil, err
	}
	log.Debugf("providerName=%+v", providerName)

	if os.Getenv(config.EnvUI) == "" && len(cmd.Options) == 0 {
		return nil, nil, fmt.Errorf("specify option")
	}
	log.Debugf("Options=%+v", cmd.Options)

	providerWithOptions, err := workspace.FindProvider(devsyConfig, providerName)
	if err != nil {
		return nil, nil, err
	}
	return devsyConfig, providerWithOptions, nil
}

func (cmd *SetCmd) saveOrPrintConfig(
	devsyConfig *config.Config,
	providerWithOptions *workspace.ProviderWithOptions,
) error {
	if cmd.Dry {
		if err := printOptions(devsyConfig, providerWithOptions, "json", true); err != nil {
			return fmt.Errorf("print options: %w", err)
		}
		return nil
	}
	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
