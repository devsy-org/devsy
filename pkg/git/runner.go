package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
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

	var outBuf, errBuf bytes.Buffer
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		cmd.Stdout = &outBuf
	}
	// Always capture stderr into errBuf so CommandError carries git's message,
	// teeing to the caller's writer when one is provided.
	if opts.Stderr != nil {
		cmd.Stderr = io.MultiWriter(opts.Stderr, &errBuf)
	} else {
		cmd.Stderr = &errBuf
	}

	err := cmd.Run()
	result := RunResult{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}
	if err != nil {
		cmdErr := &CommandError{
			Args:     opts.Args,
			ExitCode: -1,
			Stderr:   strings.TrimSpace(errBuf.String()),
			Err:      err,
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			cmdErr.ExitCode = exitErr.ExitCode()
		}
		return result, cmdErr
	}
	return result, nil
}

// defaultRunner is used by Repo when no runner is injected.
var defaultRunner Runner = execRunner{}
