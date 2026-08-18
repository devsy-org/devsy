package dockerinstall

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

type Executor struct {
	opts *InstallOptions
}

func NewExecutor(opts *InstallOptions) *Executor {
	return &Executor{opts: opts}
}

func (e *Executor) Run(ctx context.Context, shC, cmdStr string) error {
	fprintln(e.opts.stdout, cmdStr)

	if e.opts.dryRun {
		return nil
	}

	switch {
	case strings.HasPrefix(shC, "sudo"):
		//nolint:gosec // G204: cmdStr built internally from constants and opts, not external input
		return e.runCommand(exec.CommandContext(ctx, "sudo", "-E", "sh", "-c", cmdStr))
	case strings.HasPrefix(shC, "su"):
		//nolint:gosec // G204: cmdStr built internally from constants and opts, not external input
		return e.runCommand(exec.CommandContext(ctx, "su", "-c", cmdStr))
	case shC == ShellEcho:
		return nil
	default:
		//nolint:gosec // G204: cmdStr built internally from constants and opts, not external input
		return e.runCommand(exec.CommandContext(ctx, "sh", "-c", cmdStr))
	}
}

// isDpkgLockError reports whether stderr indicates another process (e.g.
// unattended-upgrades) is holding the dpkg lock, a transient condition worth
// retrying rather than a real command failure.
func isDpkgLockError(stderr string) bool {
	return strings.Contains(stderr, "Could not get lock") ||
		strings.Contains(stderr, "/var/lib/dpkg/lock")
}

// RunWithRetry runs cmdStr, retrying at RetryDelay intervals while it fails
// with a dpkg lock error, up to timeout. Any other error returns immediately.
func (e *Executor) RunWithRetry(
	ctx context.Context,
	shC, cmdStr string,
	timeout time.Duration,
) error {
	var lastErr error
	pollErr := wait.PollUntilContextTimeout(
		ctx, RetryDelay, timeout, true,
		func(ctx context.Context) (bool, error) {
			fprintln(e.opts.stdout, fmt.Sprintf("running command: %s", cmdStr))

			stderrBuf := &strings.Builder{}
			err := e.runWithStderrCapture(ctx, shC, cmdStr, stderrBuf)
			if err == nil {
				fprintln(e.opts.stdout, "command succeeded")
				return true, nil
			}

			if !isDpkgLockError(stderrBuf.String()) {
				return true, err
			}

			lastErr = err
			fprintln(e.opts.stderr, "waiting for dpkg lock to be released")
			return false, nil
		},
	)
	if pollErr != nil && lastErr != nil {
		return fmt.Errorf("timeout waiting for dpkg lock after %v: %w", timeout, lastErr)
	}
	return pollErr
}

func (e *Executor) RunCommands(ctx context.Context, shC string, cmds []string) error {
	for _, cmd := range cmds {
		if err := e.Run(ctx, shC, cmd); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) RunCommandsWithRetry(
	ctx context.Context,
	shC string,
	cmds []string,
	timeout time.Duration,
) error {
	for _, cmd := range cmds {
		if err := e.RunWithRetry(ctx, shC, cmd, timeout); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) runWithStderrCapture(
	ctx context.Context,
	shC, cmdStr string,
	stderrBuf *strings.Builder,
) error {
	fprintln(e.opts.stdout, cmdStr)

	if e.opts.dryRun {
		return nil
	}

	var cmd *exec.Cmd
	switch {
	case strings.HasPrefix(shC, "sudo"):
		//nolint:gosec // G204: cmdStr built internally from constants and opts, not external input
		cmd = exec.CommandContext(ctx, "sudo", "-E", "sh", "-c", cmdStr)
	case strings.HasPrefix(shC, "su"):
		//nolint:gosec // G204: cmdStr built internally from constants and opts, not external input
		cmd = exec.CommandContext(ctx, "su", "-c", cmdStr)
	case shC == ShellEcho:
		return nil
	default:
		//nolint:gosec // G204: cmdStr built internally from constants and opts, not external input
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}

	cmd.Stdout = e.opts.stdout
	cmd.Stderr = io.MultiWriter(e.opts.stderr, stderrBuf)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	return cmd.Run()
}

func (e *Executor) runCommand(cmd *exec.Cmd) error {
	cmd.Stdout = e.opts.stdout
	cmd.Stderr = e.opts.stderr
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	return cmd.Run()
}
