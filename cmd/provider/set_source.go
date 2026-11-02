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
	provider2 "github.com/devsy-org/devsy/pkg/provider"
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
		Short: "Set a provider's source",
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
	if len(args) == 0 {
		return fmt.Errorf("provider name is required")
	}
	providerName := args[0]

	unlock, err := provider2.LockProvider(devsyConfig.DefaultContext, providerName)
	if err != nil {
		return err
	}
	defer unlock()

	devsyConfig, err = config.LoadConfig(devsyConfig.DefaultContext, cmd.Provider)
	if err != nil {
		return err
	}

	if cmd.Version != "" {
		return cmd.runPinVersion(ctx, devsyConfig, args)
	}

	return cmd.setSource(ctx, devsyConfig, providerName, args)
}

// setSource points the provider at the source in args and optionally activates it.
// The caller holds the provider lock.
func (cmd *SetSourceCmd) setSource(
	ctx context.Context,
	devsyConfig *config.Config,
	providerName string,
	args []string,
) error {
	providerSource, err := sourceArg(args)
	if err != nil {
		return err
	}

	reporter, err := newStatusReporter(
		cmd.ResultFormat,
		os.Stdout,
		cmd.Verbosity > 0 || cmd.Debug,
		cmd.NoStatus,
	)
	if err != nil {
		return err
	}

	providerConfig, err := updateProviderSource(ctx, reporter, devsyConfig, providerSourceChange{
		name:   providerName,
		source: providerSource,
	})
	if err != nil {
		return err
	}

	log.Infof("updated provider: providerName=%s", providerConfig.Name)
	if !cmd.Use {
		log.Infof("to initialize the provider, run: devsy provider init %s", providerConfig.Name)
		// No PhaseReady: not ready until a following `provider init` runs.
		return nil
	}

	return cmd.activateProvider(ctx, devsyConfig, providerConfig, reporter)
}

func sourceArg(args []string) (string, error) {
	switch len(args) {
	case 1:
		return "", nil
	case 2:
		return args[1], nil
	default:
		return "", fmt.Errorf("specify either a local file, URL or Git repository. " +
			"E.g. devsy provider set-source my-provider " + config.ProviderPrefix + "gcloud")
	}
}

// providerSourceChange names a provider and the source to switch it to.
type providerSourceChange struct {
	name, source string
}

func updateProviderSource(
	ctx context.Context,
	reporter status.Reporter,
	devsyConfig *config.Config,
	change providerSourceChange,
) (*provider2.ProviderConfig, error) {
	providerName, providerSource := change.name, change.source
	var providerConfig *provider2.ProviderConfig
	err := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseInstallingProvider, Step: providerName},
		func(ctx context.Context) error {
			var updateErr error
			providerConfig, updateErr = workspace.UpdateProvider(
				ctx,
				devsyConfig,
				providerName,
				providerSource,
			)
			return updateErr
		},
	)
	return providerConfig, err
}

// activateProvider configures and activates a newly sourced provider,
// preserving previously user-provided option values.
func (cmd *SetSourceCmd) activateProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	providerConfig *provider2.ProviderConfig,
	reporter status.Reporter,
) error {
	return status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseReady, Step: providerConfig.Name},
		func(ctx context.Context) error {
			if err := ConfigureProvider(ctx, ProviderOptionsConfig{
				Provider:    providerConfig,
				ContextName: devsyConfig.DefaultContext,
				UserOptions: cmd.Options,
				Reporter:    reporter,
			}); err != nil {
				return fmt.Errorf("configure provider: %w", err)
			}

			return writeDefaultProvider(devsyConfig.DefaultContext, providerConfig.Name)
		},
	)
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
