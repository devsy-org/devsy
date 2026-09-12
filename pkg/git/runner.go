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
		if binary == binGit {
			return RunResult{}, &CommandError{Args: opts.Args, ExitCode: -1, Err: ErrGitNotFound}
		}
		return RunResult{}, &CommandError{
			Args: opts.Args, ExitCode: -1,
			Err: fmt.Errorf("%q binary not found in PATH", binary),
		}
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
	var streamOut *subprocess.StreamingRedactingWriter
	if opts.Stdout != nil {
		streamOut = &subprocess.StreamingRedactingWriter{Next: opts.Stdout, Redactor: stdoutRedactor}
		cmd.Stdout = io.MultiWriter(streamOut, outBuf)
	} else {
		cmd.Stdout = outBuf
	}
	errBuf := subprocess.NewBuffer(redactor)
	var streamErr *subprocess.StreamingRedactingWriter
	if opts.Stderr != nil {
		streamErr = &subprocess.StreamingRedactingWriter{Next: opts.Stderr, Redactor: redactor}
		cmd.Stderr = io.MultiWriter(streamErr, errBuf)
	} else {
		cmd.Stderr = errBuf
	}

	err := cmd.Run()
	result := RunResult{Stdout: []byte(outBuf.String()), Stderr: []byte(errBuf.String())}
	flushErr := errors.Join(flushStream(streamOut), flushStream(streamErr))
	if err == nil && flushErr != nil {
		return result, fmt.Errorf("flush git output: %w", flushErr)
	}
	if err != nil && flushErr != nil {
		err = errors.Join(err, flushErr)
	}
	if err != nil {
		cmdErr := &CommandError{
			Args:     opts.Args,
			ExitCode: -1,
			Stderr:   strings.TrimSpace(errBuf.String()),
			Err:      err,
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			cmdErr.ExitCode = exitErr.ExitCode()
		}
		return result, cmdErr
	}
	return result, nil
}

func flushStream(stream *subprocess.StreamingRedactingWriter) error {
	if stream == nil {
		return nil
	}
	return stream.Flush()
}

// defaultRunner is used by Repo when no runner is injected.
var defaultRunner Runner = execRunner{}
