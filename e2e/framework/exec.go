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

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/livetee"
)

// execWaitDelay bounds how long Wait may block on I/O after the process exits
// or is killed for context cancellation. A grandchild that escapes the
// process-group kill while holding the stdout/stderr pipes could otherwise
// block Wait forever, letting a hung command outlive its spec timeout.
const execWaitDelay = 30 * time.Second

// ExecCommand executes the command string with the devsy test binary.
func (f *Framework) ExecCommandOutput(ctx context.Context, args []string) (string, error) {
	var execOut bytes.Buffer

	//nolint:gosec // e2e harness runs its own test commands by design
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	outTee := livetee.New(&execOut, os.Stdout)
	cmd.Stdout = outTee
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	f.liveStdoutDropped.Add(outTee.Close())
	if err != nil {
		if ctx.Err() != nil {
			return execOut.String(), ctx.Err()
		}
		return "", err
	}

	return execOut.String(), nil
}

// ExecCommandStdout executes the command string with the devsy test binary.
func (f *Framework) ExecCommandStdout(ctx context.Context, args []string) error {
	//nolint:gosec // e2e harness runs its own test commands by design
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

// ExecCommandContaining executes the command string with the devsy test binary and
// fails unless its stdout contains searchString.
func (f *Framework) ExecCommandContaining(
	ctx context.Context,
	searchString string,
	args []string,
) error {
	var execOut bytes.Buffer

	//nolint:gosec // e2e harness runs its own test commands by design
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	outTee := livetee.New(&execOut, os.Stdout)
	cmd.Stdout = outTee
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	f.liveStdoutDropped.Add(outTee.Close())
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	if !strings.Contains(execOut.String(), searchString) {
		return fmt.Errorf("expected to find string %s in output", searchString)
	}

	return nil
}

// ExecCommandCapture executes the command string with the devsy test binary, and returns
// stdout, stderr, and any error that occurred.
func (f *Framework) ExecCommandCapture(ctx context.Context, args []string) (string, string, error) {
	return f.execCommandCapture(ctx, args, false)
}

func (f *Framework) ExecCommandDiagnosticCapture(
	ctx context.Context,
	args []string,
) (string, string, error) {
	return f.execCommandCapture(ctx, args, true)
}

func (f *Framework) execCommandCapture(
	ctx context.Context,
	args []string,
	diagnostic bool,
) (string, string, error) {
	var execOut bytes.Buffer
	var execErr bytes.Buffer

	//nolint:gosec // e2e harness runs its own test commands by design
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = execWaitDelay
	var redactor *redact.Redactor
	if diagnostic {
		redactor = redact.New(redact.Options{Environment: cmd.Environ()})
	}
	stdout, closeStdout := captureCommandOutput(&execOut, os.Stdout, redactor)
	stderr, closeStderr := captureCommandOutput(&execErr, os.Stderr, redactor)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	f.liveStdoutDropped.Add(closeStdout())
	f.liveStderrDropped.Add(closeStderr())
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return execOut.String(), execErr.String(), err
}

func captureCommandOutput(
	capture, live io.Writer,
	redactor *redact.Redactor,
) (io.Writer, func() int64) {
	if redactor == nil {
		tee := livetee.New(capture, live)
		return tee, tee.Close
	}
	tee := livetee.New(io.Discard, live)
	diagnostic := &liveDiagnosticCapture{tee: tee, stream: diagnosticredact.Stream(redactor)}
	return io.MultiWriter(capture, diagnostic), diagnostic.close
}

type liveDiagnosticCapture struct {
	tee    *livetee.Tee
	stream *redact.Stream
}

func (w *liveDiagnosticCapture) Write(p []byte) (int, error) {
	_, _ = io.WriteString(w.tee, w.stream.RedactChunk(string(p)))
	return len(p), nil
}

func (w *liveDiagnosticCapture) close() int64 {
	_, _ = io.WriteString(w.tee, w.stream.Flush())
	return w.tee.Close()
}

func externalCommandDiagnostic(value string) string {
	redactor := redact.New(redact.Options{Environment: os.Environ()})
	return diagnosticredact.Record(redactor, value)
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
