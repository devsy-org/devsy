package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
)

// ProviderOptionsConfig parameterizes ConfigureProvider.
//
// DiscardPriorValues controls whether previously user-provided option
// values are carried forward as defaults for prompts in the new option
// resolution. When false (default), values the user set previously seed
// the new prompts; when true, the user is asked from scratch.
//
// Note: this does NOT control stale-data pruning. The downstream
// resolver (pkg/options/resolver) always prunes keys absent from the
// new schema and re-resolves values that fail validation, regardless
// of this flag.
type ProviderOptionsConfig struct {
	Provider           *provider2.ProviderConfig
	ContextName        string
	UserOptions        []string
	DiscardPriorValues bool
	SkipRequired       bool
	SkipInit           bool
	SkipSubOptions     bool
	SingleMachine      *bool

	Reporter status.Reporter
}

func (cfg ProviderOptionsConfig) reporter() status.Reporter {
	if cfg.Reporter == nil {
		return status.Nop()
	}
	return cfg.Reporter
}

func ConfigureProvider(ctx context.Context, cfg ProviderOptionsConfig) error {
	devsyConfig, err := configureProviderOptions(ctx, cfg)
	if err != nil {
		return err
	}

	// save provider config (configureProviderOptions may have mutated state,
	// e.g. via initProvider marking the provider Initialized)
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
	devsyConfig, err := config.LoadConfig(cfg.ContextName, "")
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

	// Seed prompts with the user's previous answers unless the caller
	// explicitly wants a fresh slate. Stale keys are pruned downstream
	// by the resolver regardless of this branch.
	if !cfg.DiscardPriorValues {
		mergeExistingOptions(options, devsyConfig.ProviderOptions(cfg.Provider.Name))
	}

	// fill defaults
	reporter := cfg.reporter()
	status.Enter(reporter, status.PhaseResolvingOptions, cfg.Provider.Name)
	devsyConfig, err = options2.ResolveOptions(
		ctx, devsyConfig, cfg.Provider, options,
		cfg.SkipRequired, cfg.SkipSubOptions, cfg.SingleMachine,
	)
	if err != nil {
		err = fmt.Errorf("resolve options: %w", err)
		status.Fail(reporter, status.PhaseResolvingOptions, err)
		return nil, err
	}
	status.Leave(reporter, status.PhaseResolvingOptions, cfg.Provider.Name)

	// run init command
	if !cfg.SkipInit {
		stdout := log.Writer(log.LevelInfo)
		defer func() { _ = stdout.Close() }()

		stderr := log.Writer(log.LevelError)
		defer func() { _ = stderr.Close() }()

		status.Enter(reporter, status.PhaseRunningInit, cfg.Provider.Name)
		err = initProvider(ctx, devsyConfig, cfg.Provider, initIO{stdout: stdout, stderr: stderr})
		if err != nil {
			status.Fail(reporter, status.PhaseRunningInit, err)
			return nil, err
		}
		status.Leave(reporter, status.PhaseRunningInit, cfg.Provider.Name)
	}

	return devsyConfig, nil
}

// writeDefaultProvider reloads the config for the given context and writes providerName
// as the active context's DefaultProvider.
func writeDefaultProvider(contextName, providerName string) error {
	cfg, err := config.LoadConfig(contextName, "")
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	cfg.Current().DefaultProvider = providerName
	if err := config.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save default provider: %w", err)
	}
	return nil
}

// resolveProviderName returns the provider name from args[0] if present, else the fallback
// (typically the active context's DefaultProvider). Errors when neither is available.
func resolveProviderName(args []string, defaultProvider string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if defaultProvider == "" {
		return "", fmt.Errorf("specify a provider")
	}
	return defaultProvider, nil
}

// assertProviderMatchesGlobal returns an error when both the resolved provider name and
// the --provider global flag are set but disagree.
func assertProviderMatchesGlobal(resolved, globalFlag string) error {
	if resolved == "" || globalFlag == "" || resolved == globalFlag {
		return nil
	}
	log.Infof("providerName=%+v", resolved)
	log.Infof("GlobalFlags.Provider=%+v", globalFlag)
	return fmt.Errorf("ambiguous provider configuration detected")
}

type initIO struct {
	stdout io.Writer
	stderr io.Writer
}

func initProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider2.ProviderConfig,
	io2 initIO,
) error {
	lock, err := provider2.GetProviderInitLock(devsyConfig.DefaultContext, provider.Name)
	if err != nil {
		return fmt.Errorf("get init lock: %w", err)
	}
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock provider init: %w", err)
	}
	if !locked {
		return fmt.Errorf("provider %q is already being initialized", provider.Name)
	}
	defer func() { _ = lock.Unlock() }()

	entry := providerConfigEntry(devsyConfig, provider.Name)
	entry.InitAttempted = true
	entry.InitError = ""
	entry.Initialized = false
	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save init state: %w", err)
	}

	runErr := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Command: provider.Exec.Init,
		Context: devsyConfig.DefaultContext,
		Options: devsyConfig.ProviderOptions(provider.Name),
		Config:  provider,
		Stdout:  io2.stdout,
		Stderr:  io2.stderr,
	})
	if runErr != nil {
		entry.InitError = truncateInitError(runErr.Error())
		if saveErr := config.SaveConfig(devsyConfig); saveErr != nil {
			log.Warnf("save init failure state for provider %s: %v", provider.Name, saveErr)
		}
		return fmt.Errorf("init: %w", runErr)
	}

	entry.Initialized = true
	return nil
}

// providerConfigEntry returns the config.ProviderConfig entry for name,
// creating the Providers map and/or the entry itself if either is unset.
func providerConfigEntry(devsyConfig *config.Config, name string) *config.ProviderConfig {
	if devsyConfig.Current().Providers == nil {
		devsyConfig.Current().Providers = map[string]*config.ProviderConfig{}
	}
	if devsyConfig.Current().Providers[name] == nil {
		devsyConfig.Current().Providers[name] = &config.ProviderConfig{}
	}
	return devsyConfig.Current().Providers[name]
}

const maxInitErrorLen = 500

func truncateInitError(msg string) string {
	runes := []rune(msg)
	if len(runes) <= maxInitErrorLen {
		return msg
	}
	return string(runes[:maxInitErrorLen]) + "..."
}
