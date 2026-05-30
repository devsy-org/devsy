package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliErrors "github.com/devsy-org/devsy/pkg/errors"
	"github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

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

		err = initProvider(ctx, devsyConfig, cfg.Provider, initIO{stdout: stdout, stderr: stderr})
		if err != nil {
			return nil, err
		}
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
		return "", fmt.Errorf("please specify a provider")
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
		Stdout:  io2.stdout,
		Stderr:  io.MultiWriter(io2.stderr, stderrBuf),
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
