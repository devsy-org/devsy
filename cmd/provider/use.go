package provider

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UseProvider sets the named provider as the default for the active config context.
func UseProvider(devsyConfig *config.Config, name string) error {
	p, err := workspace.FindProvider(devsyConfig, name)
	if err != nil {
		return err
	}
	devsyConfig.Current().DefaultProvider = p.Config.Name
	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	log.Infof("default provider: %s", p.Config.Name)
	return nil
}

// UseCmd holds the cmd flags.
type UseCmd struct {
	*flags.GlobalFlags
}

// NewUseCmd creates the cobra command for `provider use`.
func NewUseCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UseCmd{GlobalFlags: f}
	defaultCmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Set the default provider for the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			p, err := workspace.FindProvider(devsyConfig, args[0])
			if err != nil {
				return err
			}
			devsyConfig.Current().DefaultProvider = p.Config.Name
			if err := config.SaveConfig(devsyConfig); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			log.Infof("default provider: %s", p.Config.Name)
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
	return defaultCmd
}
