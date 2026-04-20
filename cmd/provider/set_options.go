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
	oldlog "github.com/devsy-org/log"
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

//nolint:cyclop // pre-existing complexity
func (cmd *SetOptionsCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providerName := devsyConfig.Current().DefaultProvider
	if len(args) > 0 {
		providerName = args[0]
	} else if providerName == "" {
		return fmt.Errorf("please specify a provider")
	}
	log.Debugf("providerName=%+v", providerName)

	if os.Getenv(config.EnvUI) == "" && len(cmd.Options) == 0 {
		return fmt.Errorf("please specify option")
	}
	log.Debugf("Options=%+v", cmd.Options)

	var logger oldlog.Logger = oldlog.Default
	if cmd.Dry {
		logger = oldlog.Default.ErrorStreamOnly()
	}

	providerWithOptions, err := workspace.FindProvider(devsyConfig, providerName, logger)
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
		Log:            logger,
	})
	if err != nil {
		return err
	}

	// save provider config
	if !cmd.Dry {
		err = config.SaveConfig(devsyConfig)
		if err != nil {
			return fmt.Errorf("save config: %w", err)
		}
	} else {
		// print options to stdout
		err = printOptions(devsyConfig, providerWithOptions, "json", true)
		if err != nil {
			return fmt.Errorf("print options: %w", err)
		}
	}

	// print success message
	log.Infof("set options for provider: providerName=%s", providerWithOptions.Config.Name)
	return nil
}
