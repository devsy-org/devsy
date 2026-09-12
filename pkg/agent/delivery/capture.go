package delivery

import (
	"context"
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
