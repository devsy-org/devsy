//go:build !windows

package docker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/devsy-org/devsy/pkg/command"
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
	// A separate session lets identity-checked cleanup recognize descendants
	// after the leader has exited, without targeting a reused PID.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	startErr := cmd.Start()
	closeAll(stdinR, stdoutW, stderrW) // child ends; the child holds its own copies
	if startErr != nil {
		closeAll(stdinW, stdoutR, stderrR)
		return nil, startErr
	}
	identity, err := command.StartedSessionIdentity(cmd.Process.Pid)
	if err == nil && identity == "" {
		err = errors.New("process identity is unavailable")
	}
	if err != nil {
		killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		waitErr := cmd.Wait()
		closeAll(stdinW, stdoutR, stderrR)
		return nil, errors.Join(
			fmt.Errorf("capture docker stdio process identity: %w", err),
			killErr,
			waitErr,
		)
	}

	return &launchedStdio{
		proc:   &groupProcess{cmd: cmd, identity: identity},
		stdin:  stdinW,
		stdout: stdoutR,
		stderr: stderrR,
	}, nil
}

// groupProcess retains the session identity after exec.Cmd reaps its leader.
type groupProcess struct {
	cmd      *exec.Cmd
	identity string
}

func (p *groupProcess) PID() int { return p.cmd.Process.Pid }

func (p *groupProcess) Wait() error { return p.cmd.Wait() }

func (p *groupProcess) KillTree() error {
	return command.KillTreeWithIdentity(strconv.Itoa(p.PID()), "docker-stdio", p.identity)
}

func (p *groupProcess) Release() error {
	return command.KillTreeAfterWorkerExit(strconv.Itoa(p.PID()), "docker-stdio", p.identity)
}
