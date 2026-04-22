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

// SetOptionsCmd holds the use cmd flags.
type SetOptionsCmd struct {
	*flags.GlobalFlags

	Dry bool

	Reconfigure   bool
	SingleMachine bool
	Options       []string
}

// NewSetOptionsCmd creates a new command.
func NewSetOptionsCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &SetOptionsCmd{
		GlobalFlags: f,
	}
	setOptionsCmd := &cobra.Command{
		Use:   "set-options [provider]",
		Short: "Sets options for the given provider. Similar to 'devsy provider use', but does not switch the default provider.",
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

	setOptionsCmd.Flags().
		BoolVar(&cmd.SingleMachine, "single-machine", false, "If enabled will use a single machine for all workspaces")
	setOptionsCmd.Flags().
		BoolVar(&cmd.Reconfigure, "reconfigure", false, "If enabled will not merge existing provider config")
	setOptionsCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")
	setOptionsCmd.Flags().
		BoolVar(&cmd.Dry, "dry", false, "Dry will not persist the options to file and instead return the new filled options")
	return setOptionsCmd
}

func (cmd *SetOptionsCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providerName, err := resolveProviderName(args, devsyConfig.Current().DefaultProvider)
	if err != nil {
		return err
	}
	log.Debugf("providerName=%+v", providerName)

	if os.Getenv(config.EnvUI) == "" && len(cmd.Options) == 0 {
		return fmt.Errorf("please specify option")
	}
	log.Debugf("Options=%+v", cmd.Options)

	providerWithOptions, err := workspace.FindProvider(devsyConfig, providerName)
	if err != nil {
		return err
	}

	devsyConfig, err = configureProviderOptions(ctx, ProviderOptionsConfig{
		Provider:       providerWithOptions.Config,
		Context:        devsyConfig.DefaultContext,
		UserOptions:    cmd.Options,
		Reconfigure:    cmd.Reconfigure,
		SkipRequired:   cmd.Dry,
		SkipInit:       cmd.Dry,
		SkipSubOptions: false,
		SingleMachine:  &cmd.SingleMachine,
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

func resolveProviderName(args []string, defaultProvider string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if defaultProvider == "" {
		return "", fmt.Errorf("please specify a provider")
	}
	return defaultProvider, nil
}

func (cmd *SetOptionsCmd) saveOrPrintConfig(
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
