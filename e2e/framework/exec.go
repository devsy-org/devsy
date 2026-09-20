package framework

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
)

// execWaitDelay bounds how long Wait may block on I/O after the process exits
// or is killed for context cancellation. A grandchild that escapes the
// process-group kill while holding the stdout/stderr pipes could otherwise
// block Wait forever, letting a hung command outlive its spec timeout.
const execWaitDelay = 30 * time.Second

// ExecCommand executes the command string with the devsy test binary.
func (f *Framework) ExecCommandOutput(ctx context.Context, args []string) (string, error) {
	var execOut bytes.Buffer

	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	cmd.Stdout = io.MultiWriter(os.Stdout, &execOut)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return execOut.String(), nil
}

// ExecCommandStdout executes the command string with the devsy test binary.
func (f *Framework) ExecCommandStdout(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}

// ExecCommand executes the command string with the devsy test binary.
func (f *Framework) ExecCommand(
	ctx context.Context,
	captureStdOut, searchForString bool,
	searchString string,
	args []string,
) error {
	var execOut bytes.Buffer

	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	cmd.Stdout = io.MultiWriter(os.Stdout, &execOut)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	if captureStdOut && searchForString {
		if strings.Contains(execOut.String(), searchString) {
			return nil
		}

		return fmt.Errorf("expected to find string %s in output", searchString)
	}

	return nil
}

// ExecCommandCapture executes the command string with the devsy test binary, and returns stdout, stderr, and any error that occurred.
func (f *Framework) ExecCommandCapture(ctx context.Context, args []string) (string, string, error) {
	var execOut bytes.Buffer
	var execErr bytes.Buffer

	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	cmd.Stdout = io.MultiWriter(os.Stdout, &execOut)
	cmd.Stderr = io.MultiWriter(os.Stderr, &execErr)

	err := cmd.Run()
	return execOut.String(), execErr.String(), err
}
