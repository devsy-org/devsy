// Package subprocess provides bounded, redacted subprocess diagnostics.
package subprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
)

const (
	// MaxCapturedLines and MaxCapturedBytes bound each captured stream.
	MaxCapturedLines = 200
	MaxCapturedBytes = 1 << 20
)

// Options configures a command invocation.
type Options struct {
	Dir      string
	Env      []string
	Stdin    io.Reader
	Redactor *secrets.Redactor
	// SensitiveValues contains secret values that may appear in argv or in
	// subprocess output but are not represented by a sensitive environment
	// variable. Values are masked before capture and command rendering.
	SensitiveValues []string
	// OperationID links diagnostics to the semantic operation that spawned
	// the command. When empty, Run derives it from ctx.
	OperationID string
}

// RedactingWriter forwards output after applying redaction. It is useful when
// a subprocess must stream diagnostics while retaining the same safety policy
// as captured output.
type RedactingWriter struct {
	Next     io.Writer
	Redactor *secrets.Redactor
}

func (w RedactingWriter) Write(p []byte) (int, error) {
	if w.Next == nil {
		return len(p), nil
	}
	if w.Redactor == nil {
		return w.Next.Write(p)
	}
	redacted := []byte(w.Redactor.Redact(string(p)))
	_, err := w.Next.Write(redacted)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// StreamingRedactingWriter is the chunk-safe variant of RedactingWriter. It
// retains a short suffix between writes and must be flushed at stream end.
type StreamingRedactingWriter struct {
	Next     io.Writer
	Redactor *secrets.Redactor
	stream   *secrets.StreamingRedactor
}

func (w *StreamingRedactingWriter) Write(p []byte) (int, error) {
	if w == nil || w.Next == nil {
		return len(p), nil
	}
	if w.stream == nil {
		w.stream = secrets.NewStreamingRedactor(w.Redactor)
	}
	redacted := []byte(w.stream.RedactChunk(string(p)))
	if len(redacted) == 0 {
		return len(p), nil
	}
	_, err := w.Next.Write(redacted)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush forwards the final retained suffix. Callers that stream output should
// invoke this after the subprocess exits so trailing bytes are not delayed.
func (w *StreamingRedactingWriter) Flush() error {
	if w == nil || w.Next == nil || w.stream == nil {
		return nil
	}
	_, err := w.Next.Write([]byte(w.stream.Flush()))
	return err
}

// Result contains bounded diagnostic output and execution metadata.
type Result struct {
	Command        []string
	DisplayCommand string
	OperationID    string
	Stdout         string
	Stderr         string
	ExitCode       int
	Signal         string
	Duration       time.Duration
	Truncated      bool
}

// DiagnosticOutput returns the useful captured tail for a failed command.
// Output is already bounded and redacted; when capture reached a limit, the
// suffix is annotated so callers do not present an incomplete diagnostic as
// the complete command output.
func (r Result) DiagnosticOutput() string {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(r.Stderr); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(r.Stdout); text != "" {
		parts = append(parts, text)
	}
	output := strings.Join(parts, "\n")
	if !r.Truncated || output == "" {
		return output
	}
	return output + "\n(additional output omitted; use --verbose for full command output)"
}

// Run executes binary with args and captures bounded, redacted output from
// both streams. The returned error is the original execution error wrapped
// with the display-safe command; output remains available in Result.
func Run(ctx context.Context, binary string, args []string, options Options) (Result, error) { //nolint:cyclop // assembles the complete subprocess capture configuration
	if ctx == nil {
		ctx = context.Background()
	}
	if options.OperationID == "" {
		options.OperationID = status.OperationID(ctx)
	}
	cmd := exec.CommandContext(ctx, binary, args...) // #nosec G204 -- caller controls the intended subprocess
	cmd.Dir = options.Dir
	if options.Env != nil {
		cmd.Env = append(os.Environ(), options.Env...)
	}
	cmd.Stdin = options.Stdin
	redactor := options.Redactor
	if len(options.Env) > 0 {
		envRedactor := secrets.NewEnvironmentRedactor(options.Env)
		redactor = secrets.Combine(redactor, envRedactor)
	}
	if len(options.SensitiveValues) > 0 {
		entries := make([]string, 0, len(options.SensitiveValues))
		for i, value := range options.SensitiveValues {
			entries = append(entries, fmt.Sprintf("DEVSY_SENSITIVE_%d=%s", i, value))
		}
		redactor = secrets.Combine(redactor, secrets.NewRedactor(entries))
	}
	result, err := RunCommand(cmd, redactor)
	if err != nil && ctx.Err() != nil {
		// Keep both causes: callers can classify the stable context error while
		// still inspecting the underlying process failure and captured output.
		err = fmt.Errorf("%w: %w", ctx.Err(), err)
	}
	result.OperationID = options.OperationID
	return result, err
}

// RunCommand executes an already-configured command with bounded, redacted
// stdout and stderr capture. Existing command setup such as an explicit
// environment, working directory, or stdin is preserved.
func RunCommand(cmd *exec.Cmd, redactor *secrets.Redactor) (Result, error) {
	if cmd == nil {
		return Result{}, errors.New("subprocess command is nil")
	}
	out := NewBuffer(redactor)
	errOut := NewBuffer(redactor)
	cmd.Stdout = out
	cmd.Stderr = errOut
	binary := cmd.Path
	args := []string(nil)
	if len(cmd.Args) > 0 {
		if binary == "" {
			binary = cmd.Args[0]
		}
		args = cmd.Args[1:]
	}

	started := time.Now()
	err := cmd.Run()
	result := Result{
		Command:        append([]string(nil), cmd.Args...),
		DisplayCommand: displayCommand(binary, args, redactor),
		Stdout:         out.String(),
		Stderr:         errOut.String(),
		Duration:       time.Since(started),
		Truncated:      out.Truncated() || errOut.Truncated(),
		ExitCode:       0,
	}
	if err != nil {
		result.ExitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.Signal = processSignal(exitErr.ProcessState)
		}
		return result, fmt.Errorf("run %s: %w", result.DisplayCommand, err)
	}
	return result, nil
}

func processSignal(state *os.ProcessState) string {
	if state == nil {
		return ""
	}
	// ProcessState.String is portable and includes the signal name for a
	// signaled process (for example, "signal: killed").
	text := state.String()
	if signal, ok := strings.CutPrefix(text, "signal: "); ok {
		return signal
	}
	return ""
}

func displayCommand(binary string, args []string, redactor *secrets.Redactor) string {
	parts := append([]string{binary}, args...)
	for i := range parts {
		if redactor != nil {
			parts[i] = redactor.Redact(parts[i])
		}
		if strings.ContainsAny(parts[i], " \t\n\"") {
			parts[i] = fmt.Sprintf("%q", parts[i])
		}
	}
	return strings.Join(parts, " ")
}

// Buffer is a bounded, redacting io.Writer for subprocess output.
type Buffer struct {
	mu        sync.Mutex
	stream    *secrets.StreamingRedactor
	data      []byte
	lines     int
	truncated bool
}

// NewBuffer creates a bounded output buffer.
func NewBuffer(redactor *secrets.Redactor) *Buffer {
	return &Buffer{
		stream: secrets.NewStreamingRedactor(redactor),
		data:   make([]byte, 0, 4096),
	}
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := []byte(b.stream.RedactChunk(string(p)))
	b.appendLocked(text)
	return len(p), nil
}

func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.appendLocked([]byte(b.stream.Flush()))
	return string(b.data)
}

func (b *Buffer) appendLocked(text []byte) { //nolint:funcorder // private helper is shared by the two write methods above
	if len(text) == 0 {
		return
	}
	b.data = append(b.data, text...)
	if len(b.data) > MaxCapturedBytes {
		b.data = b.data[len(b.data)-MaxCapturedBytes:]
		b.truncated = true
	}
	if lines := strings.Count(string(b.data), "\n"); lines > MaxCapturedLines {
		cut := 0
		seen := 0
		for i, value := range slices.Backward(b.data) {
			if value == '\n' {
				seen++
				if seen == MaxCapturedLines {
					cut = i + 1
					break
				}
			}
		}
		b.data = b.data[cut:]
		b.truncated = true
	}
	b.lines = strings.Count(string(b.data), "\n")
}

func (b *Buffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
