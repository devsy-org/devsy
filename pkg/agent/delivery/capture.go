package delivery

import (
	"context"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

// runCaptured executes cmd with bounded, redacted diagnostics. The unredacted
// stdout is also preserved on the result for the callers that parse docker
// output, such as volume mountpoints, where a masked character would corrupt
// the value. Diagnostics built from the result stay redacted.
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
