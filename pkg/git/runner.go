package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

// ErrGitNotFound is returned when the git binary is not available on PATH.
var ErrGitNotFound = errors.New("git binary not found in PATH")

// CommandError describes a failed git invocation.
type CommandError struct {
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *CommandError) Error() string {
	msg := fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

// Runner executes a single git subcommand.
type Runner interface {
	Run(ctx context.Context, opts RunOptions) (RunResult, error)
}

// RunOptions describes one command invocation.
type RunOptions struct {
	Binary string
	Dir    string
	Env    []string
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// UnredactedStdout preserves machine-readable output that may contain a
	// credential, such as the response from `git credential fill`.
	UnredactedStdout bool
}

// RunResult holds captured output.
type RunResult struct {
	Stdout []byte
	Stderr []byte
}

// execRunner runs git as a subprocess.
type execRunner struct{}

var _ Runner = execRunner{}

func (execRunner) Run(ctx context.Context, opts RunOptions) (RunResult, error) {
	binary := opts.Binary
	if binary == "" {
		binary = binGit
	}
	if !command.Exists(binary) {
		return RunResult{}, missingBinaryError(binary, opts.Args)
	}

	cmd := exec.CommandContext(
		ctx,
		binary,
		opts.Args...) // #nosec G204 -- binary is an internal constant (git or a package manager)
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Dir = opts.Dir
	cmd.Stdin = opts.Stdin

	redactor := secrets.NewEnvironmentRedactor(cmd.Env)
	stdoutRedactor := redactor
	if opts.UnredactedStdout {
		stdoutRedactor = nil
	}
	outBuf := subprocess.NewBuffer(stdoutRedactor)
	streamOut := attachStream(&cmd.Stdout, outBuf, opts.Stdout, stdoutRedactor)
	errBuf := subprocess.NewBuffer(redactor)
	streamErr := attachStream(&cmd.Stderr, errBuf, opts.Stderr, redactor)

	err := cmd.Run()
	result := RunResult{Stdout: []byte(outBuf.String()), Stderr: []byte(errBuf.String())}
	flushErr := errors.Join(flushStream(streamOut), flushStream(streamErr))

	return result, finishRun(opts.Args, errBuf.String(), err, flushErr)
}

// finishRun returns the command failure, with any flush failure joined to it.
// A flush failure alone is still an error because output may be missing.
func finishRun(args []string, stderr string, runErr, flushErr error) error {
	if runErr == nil {
		if flushErr != nil {
			return fmt.Errorf("flush git output: %w", flushErr)
		}

		return nil
	}
	if flushErr != nil {
		runErr = errors.Join(runErr, flushErr)
	}

	return runFailure(args, stderr, runErr)
}

func missingBinaryError(binary string, args []string) error {
	if binary == binGit {
		return &CommandError{Args: args, ExitCode: -1, Err: ErrGitNotFound}
	}
	return &CommandError{
		Args: args, ExitCode: -1,
		Err: fmt.Errorf("%q binary not found in PATH", binary),
	}
}

// attachStream points *dst at the buffer and, when a live writer is given,
// also at a redacting stream to it. It returns the stream to flush, or nil.
func attachStream(
	dst *io.Writer,
	buf *subprocess.Buffer,
	live io.Writer,
	redactor *secrets.Redactor,
) *subprocess.StreamingRedactingWriter {
	if live == nil {
		*dst = buf
		return nil
	}
	stream := &subprocess.StreamingRedactingWriter{Next: live, Redactor: redactor}
	*dst = io.MultiWriter(stream, buf)
	return stream
}

func runFailure(args []string, stderr string, err error) *CommandError {
	cmdErr := &CommandError{
		Args:     args,
		ExitCode: -1,
		Stderr:   strings.TrimSpace(stderr),
		Err:      err,
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		cmdErr.ExitCode = exitErr.ExitCode()
	}
	return cmdErr
}

func flushStream(stream *subprocess.StreamingRedactingWriter) error {
	if stream == nil {
		return nil
	}
	return stream.Flush()
}

// defaultRunner is used by Repo when no runner is injected.
var defaultRunner Runner = execRunner{}
