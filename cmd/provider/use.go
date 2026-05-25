package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliErrors "github.com/devsy-org/devsy/pkg/errors"
	"github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UseCmd holds the use cmd flags.
type UseCmd struct {
	*flags.GlobalFlags

	Reconfigure   bool
	SingleMachine bool
	Options       []string

	// only for testing
	SkipInit bool
}

// NewUseCmd creates a new command.
func NewUseCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &UseCmd{
		GlobalFlags: flags,
	}
	useCmd := &cobra.Command{
		Use:   "use [name]",
		Short: "Configure an existing provider and set as default",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("please specify the provider to use")
			}

			return cmd.Run(cobraCmd.Context(), args[0])
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

	AddFlags(useCmd, cmd)
	return useCmd
}

func AddFlags(useCmd *cobra.Command, cmd *UseCmd) {
	useCmd.Flags().
		BoolVar(&cmd.SingleMachine, "single-machine", false, "If enabled will use a single machine for all workspaces")
	useCmd.Flags().
		BoolVar(&cmd.Reconfigure, "reconfigure", false, "If enabled will not merge existing provider config")
	useCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")

	useCmd.Flags().
		BoolVar(&cmd.SkipInit, "skip-init", false, "ONLY FOR TESTING: If true will skip init")
	_ = useCmd.Flags().MarkHidden("skip-init")
}

// Run runs the command logic.
func (cmd *UseCmd) Run(ctx context.Context, providerName string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providerWithOptions, err := workspace.FindProvider(devsyConfig, providerName)
	if err != nil {
		return err
	}

	// should reconfigure?
	shouldReconfigure := cmd.Reconfigure || len(cmd.Options) > 0 ||
		providerWithOptions.State == nil ||
		cmd.SingleMachine
	if shouldReconfigure {
		return ConfigureProvider(ctx, ProviderOptionsConfig{
			Provider:       providerWithOptions.Config,
			Context:        devsyConfig.DefaultContext,
			UserOptions:    cmd.Options,
			Reconfigure:    cmd.Reconfigure,
			SkipRequired:   false,
			SkipInit:       cmd.SkipInit,
			SkipSubOptions: false,
			SingleMachine:  &cmd.SingleMachine,
		})
	} else {
		log.Infof(
			"To reconfigure provider %s, run with '--reconfigure' to reconfigure the provider",
			providerWithOptions.Config.Name,
		)
	}

	// set options
	defaultContext := devsyConfig.Current()
	defaultContext.DefaultProvider = providerWithOptions.Config.Name

	// save provider config
	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// print success message
	log.Infof("switched default provider: providerName=%s", providerWithOptions.Config.Name)
	return nil
}

type ProviderOptionsConfig struct {
	Provider       *provider2.ProviderConfig
	Context        string
	UserOptions    []string
	Reconfigure    bool
	SkipRequired   bool
	SkipInit       bool
	SkipSubOptions bool
	SingleMachine  *bool
}

func ConfigureProvider(ctx context.Context, cfg ProviderOptionsConfig) error {
	devsyConfig, err := configureProviderOptions(ctx, cfg)
	if err != nil {
		return err
	}

	// set options
	defaultContext := devsyConfig.Current()
	defaultContext.DefaultProvider = cfg.Provider.Name

	// save provider config
	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Infof("configured provider %s", cfg.Provider.Name)
	return nil
}

func mergeExistingOptions(
	options map[string]string,
	existingOptions map[string]config.OptionValue,
) {
	for k, v := range existingOptions {
		if _, ok := options[k]; !ok && v.UserProvided {
			options[k] = v.Value
		}
	}
}

func configureProviderOptions(
	ctx context.Context,
	cfg ProviderOptionsConfig,
) (*config.Config, error) {
	devsyConfig, err := config.LoadConfig(cfg.Context, "")
	if err != nil {
		return nil, err
	}

	cfg.UserOptions = options2.InheritOptionsFromEnvironment(
		cfg.UserOptions,
		cfg.Provider.Options,
		config.EnvProviderPrefix+cfg.Provider.Name+"_",
	)

	// parse options
	options, err := provider2.ParseOptions(cfg.UserOptions)
	if err != nil {
		return nil, fmt.Errorf("parse options: %w", err)
	}

	// merge with old values
	if !cfg.Reconfigure {
		mergeExistingOptions(options, devsyConfig.ProviderOptions(cfg.Provider.Name))
	}

	// fill defaults
	devsyConfig, err = options2.ResolveOptions(
		ctx, devsyConfig, cfg.Provider, options,
		cfg.SkipRequired, cfg.SkipSubOptions, cfg.SingleMachine,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve options: %w", err)
	}

	// run init command
	if !cfg.SkipInit {
		stdout := log.Writer(log.LevelInfo)
		defer func() { _ = stdout.Close() }()

		stderr := log.Writer(log.LevelError)
		defer func() { _ = stderr.Close() }()

		err = initProvider(ctx, devsyConfig, cfg.Provider, stdout, stderr)
		if err != nil {
			return nil, err
		}
	}

	return devsyConfig, nil
}

func initProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider2.ProviderConfig,
	stdout, stderr io.Writer,
) error {
	// Capture the sub-binary's stderr in parallel with forwarding it to the
	// regular log sink so that errors.Classify has the real provider output
	// to fingerprint, not just an opaque "exit status 1".
	stderrBuf := &bytes.Buffer{}
	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "init",
		Command: provider.Exec.Init,
		Context: devsyConfig.DefaultContext,
		Options: devsyConfig.ProviderOptions(provider.Name),
		Config:  provider,
		Stdout:  stdout,
		Stderr:  io.MultiWriter(stderr, stderrBuf),
	})
	if err != nil {
		return cliErrors.Classify(fmt.Errorf("init: %w", err), cliErrors.ClassifyContext{
			Provider: provider.Name,
			Stderr:   stderrBuf.String(),
		})
	}
	if devsyConfig.Current().Providers == nil {
		devsyConfig.Current().Providers = map[string]*config.ProviderConfig{}
	}
	if devsyConfig.Current().Providers[provider.Name] == nil {
		devsyConfig.Current().Providers[provider.Name] = &config.ProviderConfig{}
	}
	devsyConfig.Current().Providers[provider.Name].Initialized = true
	return nil
}
