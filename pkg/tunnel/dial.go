package tunnel

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
)

const (
	sshSubcommand = "ssh"
	sshStdioFlag  = "--stdio"
)

type WorkspaceDialer struct {
	// DevsyBinary is the path to the devsy binary. If empty, os.Executable() is used.
	DevsyBinary string
	// Context is the Devsy context name
	Context string
	// User is the workspace user
	User string
	// Workspace is the workspace name/ID
	Workspace string
	// Workdir is the optional working directory
	Workdir string
	// GPGAgent enables GPG agent forwarding
	GPGAgent bool
}

// Dial creates a new connection to the workspace by spawning devsy ssh --stdio.
// Each call creates an independent connection, allowing multiple simultaneous
// SSH sessions (e.g. VS Code opening several connections).
func (d *WorkspaceDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	binary, args, err := d.buildCommand()
	if err != nil {
		return nil, err
	}

	// #nosec G204 -- binary is self-referencing executable
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stderr = log.Writer(log.LevelDebug)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start devsy ssh: %w", err)
	}

	return &processConn{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}, nil
}

func (d *WorkspaceDialer) buildCommand() (string, []string, error) {
	binary := d.DevsyBinary
	if binary == "" {
		var err error
		binary, err = os.Executable()
		if err != nil {
			return "", nil, fmt.Errorf("resolve devsy binary: %w", err)
		}
	}

	args := []string{sshSubcommand, sshStdioFlag}
	if d.Context != "" {
		args = append(args, "--context", d.Context)
	}
	if d.User != "" {
		args = append(args, "--user", d.User)
	}
	if d.Workdir != "" {
		args = append(args, "--workdir", d.Workdir)
	}
	if d.GPGAgent {
		args = append(args, "--gpg-agent-forwarding")
	}
	args = append(args, d.Workspace)

	return binary, args, nil
}

// processConn wraps a subprocess stdin/stdout as an io.ReadWriteCloser.
type processConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	once   sync.Once
}

func (c *processConn) Read(p []byte) (int, error) {
	return c.stdout.Read(p)
}

func (c *processConn) Write(p []byte) (int, error) {
	return c.stdin.Write(p)
}

func (c *processConn) Close() error {
	var closeErr error
	c.once.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		closeErr = c.cmd.Wait()
	})
	return closeErr
}
