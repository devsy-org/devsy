package delivery

import (
	"context"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

// runCaptured captures with redacted diagnostics, keeping the unredacted
// stdout for the callers that parse docker output such as volume mountpoints,
// where a masked character would corrupt the path.
func runCaptured(ctx context.Context, cmd *exec.Cmd) (subprocess.Result, error) {
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	result, err := subprocess.RunCommandUnredactedStdout(cmd, secrets.NewEnvironmentRedactor(env))
	result.OperationID = status.OperationID(ctx)
	return result, err
}

func capturedOutput(result subprocess.Result) string {
	return result.DiagnosticOutput()
}
