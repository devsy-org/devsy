package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"
)

type ProcessSpec struct {
	Command []string
	Env     []string
	Dir     string
	Stderr  io.Writer

	LocalAddr  net.Addr
	RemoteAddr net.Addr
}

type processConn struct {
	stdin  *os.File
	stdout *os.File
	cmd    *exec.Cmd
	cancel context.CancelFunc
	state  *managedState
}

func StartProcessConn(ctx context.Context, spec ProcessSpec) (ManagedConn, error) {
	if len(spec.Command) == 0 || spec.Command[0] == "" {
		return nil, fmt.Errorf("command is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	childStdin, parentStdin, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create process stdin pipe: %w", err)
	}
	parentStdout, childStdout, err := os.Pipe()
	if err != nil {
		_ = childStdin.Close()
		_ = parentStdin.Close()
		return nil, fmt.Errorf("create process stdout pipe: %w", err)
	}

	processCtx, cancel := context.WithCancel(ctx)
	// Command and arguments are supplied by the caller
	cmd := exec.CommandContext(processCtx, spec.Command[0], spec.Command[1:]...) // #nosec G204
	cmd.Stdin = childStdin
	cmd.Stdout = childStdout
	cmd.Stderr = spec.Stderr
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	if err := cmd.Start(); err != nil {
		cancel()
		_ = childStdin.Close()
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = childStdout.Close()
		return nil, fmt.Errorf("start process: %w", err)
	}
	_ = childStdin.Close()
	_ = childStdout.Close()

	conn := &processConn{
		stdin:  parentStdin,
		stdout: parentStdout,
		cmd:    cmd,
		cancel: cancel,
		state:  newManagedState(),
	}
	return &addrConn{Conn: conn, local: spec.LocalAddr, remote: spec.RemoteAddr}, nil
}

func (c *processConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *processConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

func (c *processConn) Close() error {
	var err error
	c.state.closeOnce.Do(func() {
		c.cancel()
		if closeErr := c.stdin.Close(); closeErr != nil {
			err = closeErr
		}
		if closeErr := c.stdout.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	})
	return err
}

func (c *processConn) LocalAddr() net.Addr              { return NewAddr("process") }
func (c *processConn) RemoteAddr() net.Addr             { return NewAddr("process") }
func (c *processConn) SetDeadline(time.Time) error      { return os.ErrInvalid }
func (c *processConn) SetReadDeadline(time.Time) error  { return os.ErrInvalid }
func (c *processConn) SetWriteDeadline(time.Time) error { return os.ErrInvalid }
func (c *processConn) PID() int                         { return c.cmd.Process.Pid }
func (c *processConn) Wait() error {
	c.state.waitOnce.Do(func() {
		c.state.waitErr = c.cmd.Wait()
		close(c.state.waitDone)
	})
	return c.state.wait()
}
