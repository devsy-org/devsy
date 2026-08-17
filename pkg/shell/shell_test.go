package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
)

func TestRunEmulatedShell_KillExecutesRealBinary(t *testing.T) {
	pid := os.Getpid()
	cmd := fmt.Sprintf("kill -0 %d", pid)

	var stdout, stderr bytes.Buffer
	err := RunEmulatedShell(context.Background(), &CommandRunner{
		Command: cmd,
		Stdout:  &stdout,
		Stderr:  &stderr,
		Environ: os.Environ(),
	})
	if err != nil {
		t.Fatalf(
			"kill -0 on own PID should succeed, got error: %v\nstderr: %s",
			err,
			stderr.String(),
		)
	}
}
