//go:build !windows

package docker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// launchDialStdio starts the child in its own process group. The stream pipes
// are plain os.Pipe files handed to the child as *os.File, so exec.Cmd spawns
// no copy goroutines and Wait never touches or closes the parent ends.
func launchDialStdio(spec launchSpec) (*launchedStdio, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW)
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW)
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Deliberately not CommandContext: the process must outlive the dial ctx.
	//nolint:gosec // command comes from trusted provider config
	cmd := exec.Command(spec.path, spec.args...)
	cmd.Env = spec.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stderrW
	setProcessGroupAttrs(cmd)

	startErr := cmd.Start()
	closeAll(stdinR, stdoutW, stderrW) // child ends; the child holds its own copies
	if startErr != nil {
		closeAll(stdinW, stdoutR, stderrR)
		return nil, startErr
	}

	return &launchedStdio{
		proc:   &groupProcess{cmd: cmd},
		stdin:  stdinW,
		stdout: stdoutR,
		stderr: stderrR,
	}, nil
}

// groupProcess adapts an exec.Cmd led by its own process group.
type groupProcess struct {
	cmd *exec.Cmd

	mu     sync.Mutex
	exited bool // guarded by mu; set once Wait has reaped the leader
}

func (p *groupProcess) PID() int { return p.cmd.Process.Pid }

func (p *groupProcess) Wait() error {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
	return err
}

// KillTree signals the whole group while the leader is still unreaped, so a
// recycled pid can never be targeted.
func (p *groupProcess) KillTree() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited {
		return nil
	}
	if err := killProcessGroup(p.cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// Release has no OS handles to free: the pipes belong to stdioConn.
func (p *groupProcess) Release() error { return nil }
