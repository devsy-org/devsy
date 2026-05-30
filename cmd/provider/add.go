package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
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
				return cobraCommand.MarkFlagRequired("name")
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

	addCmd.Flags().
		BoolVar(&cmd.SingleMachine, "single-machine", false, "If enabled will use a single machine for all workspaces")
	addCmd.Flags().
		StringVar(&cmd.Name, "name", "",
			"The name for the new provider. If not specified, the name from the provider's configuration file will be used")
	addCmd.Flags().
		StringVar(&cmd.FromExisting, "from-existing", "",
			"The name of an existing provider to use as a template. Needs to be used in conjunction with the --name flag")
	addCmd.Flags().
		BoolVar(&cmd.Use, "use", true, "If enabled will automatically activate the provider")
	addCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")

	return addCmd
}

func (cmd *AddCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	providerName := cmd.Name

	if providerName != "" {
		if provider.ProviderNameRegEx.MatchString(providerName) {
			return fmt.Errorf(
				"provider name can only include lowercase letters, numbers or dashes",
			)
		}
		if len(providerName) > 32 {
			return fmt.Errorf("provider name cannot be longer than 32 characters")
		}
	}

	var providerConfig *provider.ProviderConfig
	var options []string
	if cmd.FromExisting != "" {
		if devsyConfig.Current() == nil ||
			devsyConfig.Current().Providers[cmd.FromExisting] == nil {
			return fmt.Errorf("provider %s does not exist", cmd.FromExisting)
		}
		providerWithOptions, err := workspace.CloneProvider(
			devsyConfig,
			providerName,
			cmd.FromExisting,
		)
		if err != nil {
			return err
		}

		providerConfig = providerWithOptions.Config
		options = mergeOptions(
			providerWithOptions.Config.Options,
			providerWithOptions.State.Options,
			cmd.Options,
		)
	} else {
		if len(args) != 1 {
			return fmt.Errorf("please specify either a URL or path, " +
				"e.g. devsy provider add https://path/to/my/provider.yaml")
		}
		c, err := workspace.AddProvider(devsyConfig, providerName, args[0])
		if err != nil {
			return err
		}
		providerConfig = c
		options = cmd.Options
	}

	log.Infof("installed provider: providerName=%s", providerConfig.Name)
	if cmd.Use {
		configureErr := ConfigureProvider(ctx, ProviderOptionsConfig{
			Provider:       providerConfig,
			Context:        devsyConfig.DefaultContext,
			UserOptions:    options,
			Reconfigure:    true,
			SkipRequired:   false,
			SkipInit:       false,
			SkipSubOptions: false,
			SingleMachine:  &cmd.SingleMachine,
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

		// Write DefaultProvider explicitly after successful configure
		freshConfig, err := config.LoadConfig(cmd.Context, "")
		if err != nil {
			return fmt.Errorf("reload config: %w", err)
		}
		freshConfig.Current().DefaultProvider = providerConfig.Name
		if err := config.SaveConfig(freshConfig); err != nil {
			return fmt.Errorf("save default provider: %w", err)
		}

		return nil
	}

	log.Infof("To configure the provider, please run the following command:")
	log.Infof("devsy provider configure %s", providerConfig.Name)
	return nil
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
