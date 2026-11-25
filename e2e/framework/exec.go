package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/livetee"
)

// execWaitDelay bounds how long Wait may block on I/O after the process exits
// or is killed for context cancellation. A grandchild that escapes the
// process-group kill while holding the stdout/stderr pipes could otherwise
// block Wait forever, letting a hung command outlive its spec timeout.
const execWaitDelay = 30 * time.Second

type commandCapture int

const (
	captureNone commandCapture = iota
	captureStdout
	captureBoth
)

// ExecCommand executes the command string with the devsy test binary.
func (f *Framework) ExecCommandOutput(ctx context.Context, args []string) (string, error) {
	out, _, err := f.execCommandCapture(ctx, args, captureStdout)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return "", err
	}
	return out, nil
}

// ExecCommandStdout executes the command string with the devsy test binary.
func (f *Framework) ExecCommandStdout(ctx context.Context, args []string) error {
	_, _, err := f.execCommandCapture(ctx, args, captureNone)
	return err
}

// ExecCommandContaining executes the command string with the devsy test binary and
// fails unless its stdout contains searchString.
func (f *Framework) ExecCommandContaining(
	ctx context.Context,
	searchString string,
	args []string,
) error {
	out, _, err := f.execCommandCapture(ctx, args, captureStdout)
	if err != nil {
		return err
	}
	if !strings.Contains(out, searchString) {
		return fmt.Errorf("expected to find string %s in output", searchString)
	}

	return nil
}

// ExecCommandCapture executes the command string with the devsy test binary, and returns
// stdout, stderr, and any error that occurred.
func (f *Framework) ExecCommandCapture(ctx context.Context, args []string) (string, string, error) {
	return f.execCommandCapture(ctx, args, captureBoth)
}

func (f *Framework) ExecCommandDiagnosticCapture(
	ctx context.Context,
	args []string,
) (string, string, error) {
	return f.execCommandCapture(ctx, args, captureBoth)
}

func (f *Framework) execCommandCapture(
	ctx context.Context,
	args []string,
	capture commandCapture,
) (string, string, error) {
	var execOut bytes.Buffer
	var execErr bytes.Buffer

	//nolint:gosec // e2e harness runs its own test commands by design
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	redactor := redact.New(
		redact.Options{Environment: cmd.Environ(), Values: registeredSecretValues()},
	)
	var outWriter, errWriter io.Writer = &execOut, &execErr
	switch capture {
	case captureNone:
		outWriter, errWriter = io.Discard, io.Discard
	case captureStdout:
		errWriter = io.Discard
	}
	stdout, closeStdout := captureCommandOutput(outWriter, os.Stdout, redactor)
	stderr, closeStderr := captureCommandOutput(errWriter, os.Stderr, redactor)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	f.liveStdoutDropped.Add(closeStdout())
	f.liveStderrDropped.Add(closeStderr())
	if capture != captureNone && err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return execOut.String(), execErr.String(), err
}

func captureCommandOutput(
	capture, live io.Writer,
	redactor *redact.Redactor,
) (io.Writer, func() int64) {
	tee := livetee.New(io.Discard, live)
	diagnostic := newLiveDiagnosticCapture(tee, redactor)
	return io.MultiWriter(capture, diagnostic), diagnostic.close
}

func externalCommandDiagnostic(value string) string {
	redactor := redact.New(
		redact.Options{Environment: os.Environ(), Values: registeredSecretValues()},
	)
	return ReportText(redactor, value)
}

// ExecCommandJSON runs the devsy test binary and decodes its stdout as a JSON object.
func (f *Framework) ExecCommandJSON(ctx context.Context, args []string) (map[string]any, error) {
	stdout, stderr, err := f.ExecCommandDiagnosticCapture(ctx, args)
	if err != nil {
		return nil, fmt.Errorf(
			"devsy %s: %w (stderr: %s)",
			externalCommandDiagnostic(
				strings.Join(args, " "),
			),
			err,
			externalCommandDiagnostic(stderr),
		)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return nil, fmt.Errorf("output should be valid JSON: %w", err)
	}

	return result, nil
}
