package ide

import (
	"context"
	"maps"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	"github.com/spf13/cobra"
)

// UseCmd holds the use cmd flags.
type UseCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewUseCmd creates a new command.
func NewUseCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &UseCmd{
		GlobalFlags: flags,
	}
	useCmd := &cobra.Command{
		Use:   "use <ide>",
		Short: "Configure the default IDE to use (list available IDEs with 'devsy ide list')",
		Long: `Configure the default IDE to use

Available IDEs can be listed with 'devsy ide list'`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: ideNameCompletion,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	cliflags.Add(
		useCmd,
		cliflags.StringArray(&cmd.Options, names.Option, []string{}, "IDE option in the form KEY=VALUE").
			Shorthand("o"),
	)
	return useCmd
}

// Run runs the command logic.
func (cmd *UseCmd) Run(ctx context.Context, ide string) error {
	ide = strings.ToLower(ide)
	ideOptions, err := ideparse.GetIDEOptions(ide)
	if err != nil {
		return err
	}

	err = config.UpdateConfig(cmd.Context, cmd.Provider, func(devsyConfig *config.Config) error {
		if len(cmd.Options) > 0 {
			if err := setOptions(devsyConfig, ide, cmd.Options, ideOptions); err != nil {
				return err
			}
		}

		devsyConfig.Current().DefaultIDE = ide
		return nil
	})
	if err != nil {
		return err
	}

	log.Infof("default IDE set to %q", ide)
	return nil
}

func setOptions(
	devsyConfig *config.Config,
	ide string,
	userOptions []string,
	ideOptions ide.Options,
) error {
	userOptions = options2.InheritOptionsFromEnvironment(
		userOptions,
		ideOptions,
		config.EnvIDEPrefix+ide+"_",
	)

	optionValues, err := ideparse.ParseOptions(userOptions, ideOptions)
	if err != nil {
		return err
	}

	if devsyConfig.Current().IDEs == nil {
		devsyConfig.Current().IDEs = map[string]*config.IDEConfig{}
	}

	newValues := map[string]config.OptionValue{}
	if devsyConfig.Current().IDEs[ide] != nil {
		maps.Copy(newValues, devsyConfig.Current().IDEs[ide].Options)
	}
	maps.Copy(newValues, optionValues)

	devsyConfig.Current().IDEs[ide] = &config.IDEConfig{
		Options: newValues,
	}
	return nil
}
