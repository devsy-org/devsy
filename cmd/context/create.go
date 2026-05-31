package context

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// CreateCmd holds the create cmd flags.
type CreateCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewCreateCmd creates a new command.
func NewCreateCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &CreateCmd{
		GlobalFlags: flags,
	}
	createCmd := &cobra.Command{
		Use:   "create CONTEXT",
		Short: "Create a new Devsy context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	createCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "context option in the form KEY=VALUE")
	return createCmd
}

// Run runs the command logic.
func (cmd *CreateCmd) Run(ctx context.Context, context string) error {
	devsyConfig, err := config.LoadConfig("", cmd.Provider)
	if err != nil {
		return err
	} else if devsyConfig.Contexts[context] != nil {
		return fmt.Errorf("context '%s' already exists", context)
	}

	// verify name
	if provider2.ProviderNameRegEx.MatchString(context) {
		return fmt.Errorf("context name can only include lower case letters, numbers or dashes")
	} else if len(context) > 48 {
		return fmt.Errorf("context name cannot be longer than 48 characters")
	}
	devsyConfig.Contexts[context] = &config.ContextConfig{}

	// check if there are create options set
	if len(cmd.Options) > 0 {
		err = setOptions(devsyConfig, context, cmd.Options)
		if err != nil {
			return err
		}
	}

	devsyConfig.DefaultContext = context
	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}

func setOptions(devsyConfig *config.Config, context string, options []string) error {
	optionValues, err := parseOptions(options)
	if err != nil {
		return err
	} else if devsyConfig.Contexts[context] == nil {
		return fmt.Errorf("context '%s' doesn't exist", context)
	}

	newValues := map[string]config.OptionValue{}
	if devsyConfig.Contexts[context].Options != nil {
		maps.Copy(newValues, devsyConfig.Contexts[context].Options)
	}
	maps.Copy(newValues, optionValues)

	devsyConfig.Contexts[context].Options = newValues
	return nil
}

func parseOptions(options []string) (map[string]config.OptionValue, error) {
	allowedOptions := []string{}
	contextOptions := map[string]config.ContextOption{}
	for _, option := range config.ContextOptions {
		allowedOptions = append(allowedOptions, option.Name)
		contextOptions[option.Name] = option
	}

	retMap := map[string]config.OptionValue{}
	for _, option := range options {
		splitted := strings.Split(option, "=")
		if len(splitted) == 1 {
			return nil, fmt.Errorf("invalid option '%s', expected format KEY=VALUE", option)
		}

		key := strings.ToUpper(strings.TrimSpace(splitted[0]))
		value := strings.Join(splitted[1:], "=")
		contextOption, ok := contextOptions[key]
		if !ok {
			return nil, fmt.Errorf(
				"invalid option '%s', allowed options are: %v",
				key,
				allowedOptions,
			)
		}

		if len(contextOption.Enum) > 0 {
			found := slices.Contains(contextOption.Enum, value)
			if !found {
				return nil, fmt.Errorf(
					"invalid value '%s' for option '%s', has to match one of the following values: %v",
					value,
					key,
					contextOption.Enum,
				)
			}
		}

		retMap[key] = config.OptionValue{
			Value:        value,
			UserProvided: true,
		}
	}

	return retMap, nil
}
