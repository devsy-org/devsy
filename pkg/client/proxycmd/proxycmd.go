// Proxycmd provides a interface for running provider binaries with the
// correct environment and error handling.
package proxycmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"

	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/types"
)

// Options configures a single proxy-command invocation: which provider
// binary command to exec, the options environment it runs with, and how
// errors and stderr are reported.
type Options struct {
	DevsyConfig *config.Config
	Provider    *provider.ProviderConfig

	// Command is the command to run, as a list of arguments. The first
	// argument is the binary to exec, and the rest are its arguments.
	Command types.StrArray

	// ExtraOptions are additional options to pass to the provider binary. They
	// are merged with the provider options from the devsy config, and take
	// precedence over them.
	ExtraOptions map[string]config.OptionValue

	// Stderr is the writer to which the provider binary's stderr is written. If
	// nil, it defaults to the logger's error writer.
	Stderr io.Writer
}

// Run runs a proxy command and returns its raw stdout.
func Run(ctx context.Context, cfg Options) ([]byte, error) {
	opts := cfg.DevsyConfig.ProviderOptions(cfg.Provider.Name)
	maps.Copy(opts, cfg.ExtraOptions)

	stderr := cfg.Stderr
	if stderr == nil {
		stderr = log.Writer(log.LevelError)
	}

	var stdout bytes.Buffer
	err := clientimplementation.RunCommandWithBinaries(
		ctx,
		clientimplementation.WorkspaceCommandConfig{
			Command:              cfg.Command,
			WorkspaceContextName: cfg.DevsyConfig.DefaultContext,
			Options:              opts,
			Config:               cfg.Provider,
			Stdout:               &stdout,
			Stderr:               stderr,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("with provider %q: %w", cfg.Provider.Name, err)
	}
	return stdout.Bytes(), nil
}

// RunAndPrint runs a proxy command and prints its stdout to the logger's info writer.
func RunAndPrint(ctx context.Context, cfg Options) error {
	out, err := Run(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Print(string(out)) //nolint:forbidigo // CLI stdout output
	return nil
}

func RunAndPrintTable(
	ctx context.Context,
	cfg Options,
	headers []string,
	buildRows func(payload []byte) ([][]string, error),
) error {
	out, err := Run(ctx, cfg)
	if err != nil {
		return err
	}
	return printTable(headers, out, buildRows)
}

func printTable(
	headers []string,
	payload []byte,
	buildRows func(payload []byte) ([][]string, error),
) error {
	if len(payload) == 0 {
		table.Print(headers, nil)
		return nil
	}

	rows, err := buildRows(payload)
	if err != nil {
		return fmt.Errorf("parse output: %w", err)
	}
	table.Print(headers, rows)
	return nil
}
