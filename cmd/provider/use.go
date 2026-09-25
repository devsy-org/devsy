package provider

import (
	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UseProvider sets the named provider as the default for the addressed config
// context, loading and saving config.yaml under the config lock.
func UseProvider(contextOverride, providerOverride, name string) error {
	var resolved string
	err := config.UpdateConfig(
		contextOverride,
		providerOverride,
		func(devsyConfig *config.Config) error {
			p, err := workspace.FindProvider(devsyConfig, name)
			if err != nil {
				return err
			}
			devsyConfig.Current().DefaultProvider = p.Config.Name
			resolved = p.Config.Name
			return nil
		},
	)
	if err != nil {
		return err
	}
	log.Infof("default provider: %s", resolved)
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
			return UseProvider(cmd.Context, cmd.Provider, args[0])
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
