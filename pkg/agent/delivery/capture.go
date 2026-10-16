package delivery

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

func runCaptured(ctx context.Context, cmd *exec.Cmd) (subprocess.Result, error) {
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	result, err := subprocess.RunCommand(cmd, secrets.NewEnvironmentRedactor(env))
	result.OperationID = status.OperationID(ctx)
	return result, err
}

func capturedOutput(result subprocess.Result) string {
	return result.DiagnosticOutput()
}

// dockerCLI builds a docker/podman invocation. A non-nil env is appended to
// the parent environment.
func dockerCLI(ctx context.Context, command string, env []string, args ...string) *exec.Cmd {
	// #nosec G204 -- args are constructed internally, not from user input
	cmd := exec.CommandContext(ctx, command, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

// runDockerCLI runs a docker/podman command and wraps a failure with its
// captured output.
func runDockerCLI(ctx context.Context, cmd *exec.Cmd) error {
	result, err := runCaptured(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", capturedOutput(result), err)
	}
	return nil
}
