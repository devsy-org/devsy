// Package docker is a pool provider backed by the Docker CLI. Nothing in the
// product registers it yet, so no command or config reaches it.
package docker

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultMaxOutput = 64 << 20
	maxStderr        = 4 << 10
	killWaitDelay    = 5 * time.Second
)

// ErrOutputTooLarge reports that a command printed more than the limit.
var ErrOutputTooLarge = errors.New("docker output exceeds the limit")

// Runner runs one docker command and returns its standard output. It returns
// ctx.Err() when ctx ends before the command does. Callers
// pass every user-derived value as its own argument, after "--" where the
// command takes operands, and never through a shell.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// CommandError is a failed docker command. Only the first two arguments
// (the noun and verb) are kept, so a message never carries operands.
type CommandError struct {
	Command string
	Stderr  string
	Err     error
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("docker %s: %v", e.Command, e.Err)
	}
	return fmt.Sprintf("docker %s: %v: %s", e.Command, e.Err, e.Stderr)
}

func (e *CommandError) Unwrap() error { return e.Err }

// CLI runs the "docker" binary found on PATH. The zero value keeps up to
// 64 MiB of output.
type CLI struct {
	MaxOutput int
}

// Run starts the command, kills it when ctx ends and returns ctx.Err() then.
// It builds the exec.Cmd directly so the arguments are never a shell string.
func (c CLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := exec.LookPath("docker")
	if err != nil {
		return nil, err
	}
	out := &capped{max: cmp.Or(c.MaxOutput, defaultMaxOutput)}
	errOut := &capped{max: maxStderr}
	cmd := &exec.Cmd{
		Path:      path,
		Args:      append([]string{"docker"}, args...),
		Stdout:    out,
		Stderr:    errOut,
		WaitDelay: killWaitDelay,
	}
	runErr := runKilledOnDone(ctx, cmd)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, &CommandError{
			Command: command(args),
			Stderr:  strings.TrimSpace(errOut.String()),
			Err:     runErr,
		}
	}
	if out.over {
		return nil, fmt.Errorf("docker %s: %w", command(args), ErrOutputTooLarge)
	}
	return out.Bytes(), nil
}

func runKilledOnDone(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
	defer stop()
	return cmd.Wait()
}

func command(args []string) string {
	return strings.Join(args[:min(len(args), 2)], " ")
}

// capped keeps the first max bytes and drops the rest while still reading, so
// a chatty child never blocks on a full pipe.
type capped struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	room := max(c.max-c.buf.Len(), 0)
	if n > room {
		c.over = true
		p = p[:room]
	}
	c.buf.Write(p)
	return n, nil
}

func (c *capped) Bytes() []byte  { return c.buf.Bytes() }
func (c *capped) String() string { return c.buf.String() }
