package provider

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// SetSourceCmd holds the cmd flags.
type SetSourceCmd struct {
	*flags.GlobalFlags

	Use     bool
	Version string
	Options []string
}

// NewSetSourceCmd creates a new command.
func NewSetSourceCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetSourceCmd{
		GlobalFlags: flags,
	}
	setSourceCmd := &cobra.Command{
		Use:   "set-source [name] [name, GitHub link, URL or path]",
		Short: "Set or change a provider's source (replaces the registered name, repo, URL, or path)",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			return cmd.Run(ctx, devsyConfig, args)
		},
	}

	setSourceCmd.Flags().
		BoolVar(&cmd.Use, "use", true, "If enabled will automatically activate the provider")
	setSourceCmd.Flags().
		StringVar(&cmd.Version, "version", "", "Pin the provider to a specific version tag")
	setSourceCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")
	return setSourceCmd
}

func (cmd *SetSourceCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	if cmd.Version != "" {
		return cmd.runPinVersion(devsyConfig, args)
	}

	if len(args) != 1 && len(args) != 2 {
		return fmt.Errorf("specify either a local file, URL or Git repository. " +
			"E.g. devsy provider set-source my-provider " + config.ProviderPrefix + "gcloud")
	}

	providerSource := ""
	if len(args) == 2 {
		providerSource = args[1]
	}

	providerConfig, err := workspace.UpdateProvider(devsyConfig, args[0], providerSource)
	if err != nil {
		return err
	}

	log.Infof("updated provider: providerName=%s", providerConfig.Name)
	if !cmd.Use {
		log.Infof("To initialize the provider, run: devsy provider init %s", providerConfig.Name)
		return nil
	}

	// Preserve previously user-provided values (default DiscardPriorValues=false).
	// The resolver prunes keys absent from the new schema and re-resolves values
	// that fail validation, so stale data cannot leak through this path.
	if err := ConfigureProvider(ctx, ProviderOptionsConfig{
		Provider:    providerConfig,
		ContextName: devsyConfig.DefaultContext,
		UserOptions: cmd.Options,
	}); err != nil {
		return fmt.Errorf("configure provider: %w", err)
	}

	return writeDefaultProvider(cmd.Context, providerConfig.Name)
}

func (cmd *SetSourceCmd) runPinVersion(devsyConfig *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("provider name must be provided when using --version")
	}
	if len(args) > 1 {
		return fmt.Errorf("--version and a source argument are mutually exclusive")
	}
	providerName := args[0]
	if err := workspace.SetProviderVersion(devsyConfig, providerName, cmd.Version); err != nil {
		return err
	}
	log.Infof("pinned provider %s to version %s", providerName, cmd.Version)
	return nil
}
