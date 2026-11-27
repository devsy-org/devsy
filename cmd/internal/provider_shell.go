package cmdinternal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/interp"
)

type providerShellFlags struct {
	scriptPath, forcePath, ackPath string
	executablePaths                []string
	parentPID                      int
}

func (f *providerShellFlags) invocation() (shell.ProviderShellInvocation, error) {
	paths, err := parseExecutablePaths(f.executablePaths)
	if err != nil {
		return shell.ProviderShellInvocation{}, err
	}
	return shell.ProviderShellInvocation{
		ScriptPath: f.scriptPath,
		ForcePath:  f.forcePath,
		AckPath:    f.ackPath,
		ParentPID:  f.parentPID,

		ExecutablePaths: paths,
	}, nil
}

func (f *providerShellFlags) register(command *cobra.Command) {
	command.Flags().StringVar(&f.scriptPath, "script-file", "", "Private provider script file")
	command.Flags().StringVar(&f.forcePath, "force-file", "", "Private provider force marker")
	command.Flags().StringVar(&f.ackPath, "ack-file", "", "Private provider acknowledgement marker")
	command.Flags().StringArrayVar(
		&f.executablePaths, "executable-path", nil, "Provider binary variable as NAME=PATH",
	)
	_ = command.Flags().MarkHidden("executable-path")
	command.Flags().IntVar(&f.parentPID, "parent-pid", 0, "Provider parent process ID")
	_ = command.Flags().MarkHidden("parent-pid")
	_ = command.MarkFlagRequired("parent-pid")
	_ = command.MarkFlagRequired("script-file")
	_ = command.MarkFlagRequired("force-file")
	_ = command.MarkFlagRequired("ack-file")
}

type providerShellRunner func(context.Context, shell.ProviderShellInvocation) error

func NewProviderShellCmd() *cobra.Command {
	return newProviderShellCmd(runProviderShellForeground)
}

func newProviderShellCmd(run providerShellRunner) *cobra.Command {
	flags := &providerShellFlags{}
	command := &cobra.Command{
		Use: "provider-shell", Hidden: true, Args: cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			invocation, err := flags.invocation()
			if err != nil {
				return err
			}
			err = run(cmd.Context(), invocation)
			if status, ok := errors.AsType[interp.ExitStatus](err); ok {
				os.Exit(int(status))
			}
			if errors.Is(err, context.Canceled) {
				os.Exit(1)
			}
			return shell.WithLaunchHint(err)
		},
	}
	flags.register(command)
	return command
}

func runProviderShellForeground(
	ctx context.Context,
	invocation shell.ProviderShellInvocation,
) error {
	cleanup, err := shell.PrepareProviderShellForeground(invocation.ParentPID)
	if err != nil {
		return err
	}
	defer cleanup()
	return shell.RunProviderShell(ctx, invocation)
}

func parseExecutablePaths(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	paths := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, path, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid executable path %q: want NAME=PATH", entry)
		}
		paths[name] = path
	}
	return paths, nil
}
