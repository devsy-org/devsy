package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
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

	cliflags.Add(setSourceCmd,
		cliflags.Bool(&cmd.Use, names.Use, true,
			"If enabled will automatically activate the provider"),
		cliflags.String(&cmd.Version, names.Version, "",
			"Pin the provider to a specific version tag"),
		cliflags.StringArray(&cmd.Options, names.Option, nil,
			"Provider option in the form KEY=VALUE").Shorthand("o"),
	)
	return setSourceCmd
}

func (cmd *SetSourceCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	if cmd.Version != "" {
		return cmd.runPinVersion(ctx, devsyConfig, args)
	}

	if len(args) != 1 && len(args) != 2 {
		return fmt.Errorf("specify either a local file, URL or Git repository. " +
			"E.g. devsy provider set-source my-provider " + config.ProviderPrefix + "gcloud")
	}

	providerSource := ""
	if len(args) == 2 {
		providerSource = args[1]
	}

	reporter, err := newStatusReporter(cmd.ResultFormat, os.Stdout)
	if err != nil {
		return err
	}

	status.Enter(reporter, status.PhaseInstallingProvider, args[0])
	providerConfig, err := workspace.UpdateProvider(ctx, devsyConfig, args[0], providerSource)
	if err != nil {
		status.Fail(reporter, status.PhaseInstallingProvider, err)
		return err
	}
	status.Leave(reporter, status.PhaseInstallingProvider, providerConfig.Name)

	log.Infof("updated provider: providerName=%s", providerConfig.Name)
	if !cmd.Use {
		log.Infof("to initialize the provider, run: devsy provider init %s", providerConfig.Name)
		// No PhaseReady: not ready until a following `provider init` runs.
		return nil
	}

	return cmd.activateProvider(ctx, devsyConfig, providerConfig, reporter)
}

// activateProvider configures and activates a newly sourced provider,
// preserving previously user-provided option values.
func (cmd *SetSourceCmd) activateProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	providerConfig *provider.ProviderConfig,
	reporter status.Reporter,
) error {
	if err := ConfigureProvider(ctx, ProviderOptionsConfig{
		Provider:    providerConfig,
		ContextName: devsyConfig.DefaultContext,
		UserOptions: cmd.Options,
		Reporter:    reporter,
	}); err != nil {
		return fmt.Errorf("configure provider: %w", err)
	}

	if err := writeDefaultProvider(cmd.Context, providerConfig.Name); err != nil {
		return err
	}
	status.Leave(reporter, status.PhaseReady, providerConfig.Name)
	return nil
}

func (cmd *SetSourceCmd) runPinVersion(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) error {
	if len(args) == 0 {
		return fmt.Errorf("provider name must be provided when using --version")
	}
	if len(args) > 1 {
		return fmt.Errorf("--version and a source argument are mutually exclusive")
	}
	providerName := args[0]
	if err := workspace.SetProviderVersion(
		ctx,
		devsyConfig,
		providerName,
		cmd.Version,
	); err != nil {
		return err
	}
	log.Infof("pinned provider %s to version %s", providerName, cmd.Version)
	return nil
}
