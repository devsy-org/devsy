package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// AddCmd holds the cmd flags.
type AddCmd struct {
	*flags.GlobalFlags

	Use           bool
	SingleMachine bool
	Options       []string

	Name         string
	FromExisting string
}

// NewAddCmd creates a new command.
func NewAddCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &AddCmd{
		GlobalFlags: f,
	}
	addCmd := &cobra.Command{
		Use:   "add [name, GitHub link, URL or path]",
		Short: "Adds a new provider to Devsy",
		Args:  cobra.MaximumNArgs(1),
		PreRunE: func(cobraCommand *cobra.Command, args []string) error {
			if cmd.FromExisting != "" {
				return cobraCommand.MarkFlagRequired(names.Name)
			}

			return nil
		},
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			return cmd.Run(ctx, devsyConfig, args)
		},
	}

	cliflags.Add(addCmd,
		cliflags.Bool(&cmd.SingleMachine, names.SingleMachine, false,
			"If enabled will use a single machine for all workspaces"),
		cliflags.String(
			&cmd.Name,
			names.Name,
			"",
			"The name for the new provider. If not specified, the name from the provider's configuration file will be used",
		),
		cliflags.String(
			&cmd.FromExisting,
			names.FromExisting,
			"",
			"The name of an existing provider to use as a template. Needs to be used in conjunction with the --name flag",
		),
		cliflags.Bool(&cmd.Use, names.Use, true,
			"If enabled will automatically activate the provider"),
		cliflags.StringArray(&cmd.Options, names.Option, nil,
			"Provider option in the form KEY=VALUE").Shorthand("o"),
	)

	return addCmd
}

func (cmd *AddCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	providerName := cmd.Name

	if err := validateOptionalProviderName(providerName); err != nil {
		return err
	}

	reporter, err := newStatusReporter(cmd.ResultFormat, os.Stdout)
	if err != nil {
		return err
	}

	status.Enter(reporter, status.PhaseInstallingProvider, providerName)
	providerConfig, options, err := cmd.resolveProviderConfig(ctx, devsyConfig, providerName, args)
	if err != nil {
		status.Fail(reporter, status.PhaseInstallingProvider, err)
		return err
	}
	status.Leave(reporter, status.PhaseInstallingProvider, providerConfig.Name)

	log.Infof("installed provider: providerName=%s", providerConfig.Name)
	if !cmd.Use {
		log.Infof("to initialize the provider, run: devsy provider init %s", providerConfig.Name)
		// No PhaseReady: installed but not initialized.
		return nil
	}

	if err := cmd.useProvider(ctx, devsyConfig, providerConfig, options, reporter); err != nil {
		return err
	}
	status.Leave(reporter, status.PhaseReady, providerConfig.Name)
	return nil
}

func validateOptionalProviderName(providerName string) error {
	if providerName == "" {
		return nil
	}
	if provider.ProviderNameRegEx.MatchString(providerName) {
		return fmt.Errorf(
			"provider name can only include lowercase letters, numbers or dashes",
		)
	}
	if len(providerName) > 32 {
		return fmt.Errorf("provider name cannot be longer than 32 characters")
	}
	return nil
}

func (cmd *AddCmd) resolveProviderConfig(
	ctx context.Context,
	devsyConfig *config.Config,
	providerName string,
	args []string,
) (*provider.ProviderConfig, []string, error) {
	if cmd.FromExisting != "" {
		if devsyConfig.Current() == nil ||
			devsyConfig.Current().Providers[cmd.FromExisting] == nil {
			return nil, nil, fmt.Errorf("provider %s does not exist", cmd.FromExisting)
		}
		providerWithOptions, err := workspace.CloneProvider(
			ctx,
			devsyConfig,
			providerName,
			cmd.FromExisting,
		)
		if err != nil {
			return nil, nil, err
		}

		return providerWithOptions.Config, mergeOptions(
			providerWithOptions.Config.Options,
			providerWithOptions.State.Options,
			cmd.Options,
		), nil
	}

	if len(args) != 1 {
		return nil, nil, fmt.Errorf("specify either a URL or path, " +
			"e.g. devsy provider add https://path/to/my/provider.yaml")
	}
	c, err := workspace.AddProvider(ctx, devsyConfig, providerName, args[0])
	if err != nil {
		return nil, nil, err
	}
	return c, cmd.Options, nil
}

func (cmd *AddCmd) useProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	providerConfig *provider.ProviderConfig,
	options []string,
	reporter status.Reporter,
) error {
	// First add: there are no prior user values to merge, so
	// DiscardPriorValues is moot. Set it explicitly so future readers
	// don't wonder whether merging matters here.
	configureErr := ConfigureProvider(ctx, ProviderOptionsConfig{
		Provider:           providerConfig,
		ContextName:        devsyConfig.DefaultContext,
		UserOptions:        options,
		DiscardPriorValues: true,
		SingleMachine:      &cmd.SingleMachine,
		Reporter:           reporter,
	})
	if configureErr != nil {
		devsyConfig, err := config.LoadConfig(cmd.Context, "")
		if err != nil {
			return err
		}

		err = DeleteProvider(ctx, devsyConfig, providerConfig.Name, true, true)
		if err != nil {
			return fmt.Errorf("delete provider: %w", err)
		}

		return fmt.Errorf("configure provider: %w", configureErr)
	}

	return writeDefaultProvider(cmd.Context, providerConfig.Name)
}

// mergeOptions combines user options with existing options, user provided options take precedence.
func mergeOptions(
	desiredOptions map[string]*types.Option,
	stateOptions map[string]config.OptionValue,
	userOptions []string,
) []string {
	retOptions := []string{}
	for key := range desiredOptions {
		userOption, ok := getUserOption(userOptions, key)
		if ok {
			retOptions = append(retOptions, userOption)
			continue
		}
		stateOption, ok := stateOptions[key]
		if !ok {
			continue
		}
		retOptions = append(retOptions, fmt.Sprintf("%s=%s", key, stateOption.Value))
	}

	return retOptions
}

func getUserOption(allOptions []string, optionKey string) (string, bool) {
	if len(allOptions) == 0 {
		return "", false
	}

	for _, option := range allOptions {
		splitted := strings.Split(option, "=")
		if len(splitted) == 1 {
			// ignore
			continue
		}
		if splitted[0] == optionKey {
			return option, true
		}
	}

	return "", false
}
