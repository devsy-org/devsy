//go:build linux

package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/pty"
	"github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/devsy-org/ssh"
)

type interactiveLaunch struct {
	binary   string
	args     []string
	identity interactiveIdentity
}

type interactiveIdentity struct{ credentials server.ExecCredentials }

func loginShell(name string) (string, error) {
	lookup, _, _ := strings.Cut(name, ":")
	path, err := exec.LookPath("getent")
	if err == nil {
		cmd := &exec.Cmd{Path: path, Args: []string{"getent", "passwd", lookup}}
		if out, err := cmd.Output(); err == nil {
			if shell := shellFromPasswd(string(out), lookup); shell != "" {
				return shell, nil
			}
		}
	}
	data, err := os.ReadFile("/etc/passwd")
	if err == nil {
		if shell := shellFromPasswd(string(data), lookup); shell != "" {
			return shell, nil
		}
	}
	return "", fmt.Errorf("cannot resolve login shell for %q", lookup)
}

func shellFromPasswd(data, name string) string {
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 7 && (fields[0] == name || fields[2] == name) {
			return fields[6]
		}
	}
	return ""
}

func prepareInteractiveLaunch(command []string, user string) (interactiveLaunch, error) {
	shell, err := loginShell(user)
	if err != nil {
		return interactiveLaunch{}, err
	}
	credentials, err := server.ResolveExecCredentials(user)
	if err != nil {
		return interactiveLaunch{}, fmt.Errorf("resolve execution identity: %w", err)
	}
	args := command
	if len(command) == 1 {
		if credentials.MatchCurrent {
			shell = "sh"
		}
		args = []string{shell, "-c", command[0]}
	}
	return interactiveLaunch{
		binary:   args[0],
		args:     args,
		identity: interactiveIdentity{credentials: credentials},
	}, nil
}

func runInteractiveProcess(
	ctx context.Context,
	p hookRunParams,
	launch interactiveLaunch,
	a tunnel.TerminalAttachment,
) (subprocess.Result, error) {
	result := subprocess.Result{ExitCode: -1}
	input, ok := a.Input().(io.ReadCloser)
	if !ok {
		return result, errors.New("terminal input cannot be closed")
	}
	terminal, process, err := startInteractive(p, launch, a)
	if err != nil {
		return result, err
	}

	inputDone := make(chan struct{})
	outputDone := make(chan error, 1)
	waitDone := make(chan error, 1)
	resizeDone := make(chan struct{})
	stopResize := make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(terminal.InputWriter(), input) }()
	go func() { _, copyErr := io.Copy(a.Output(), terminal.OutputReader()); outputDone <- copyErr }()
	go func() { waitDone <- process.Wait() }()
	go resizeInteractive(terminal, a, stopResize, resizeDone)
	state := &interactiveWait{waitDone: waitDone, outputDone: outputDone}
	state.wait(ctx, a)
	if state.sessionErr != nil {
		state.stop(process)
	}

	close(stopResize)
	<-resizeDone
	_ = terminal.Close()
	if !state.waited {
		state.waitErr = <-state.waitDone
	}
	if !state.drained {
		<-state.outputDone
	}
	_ = input.Close()
	<-inputDone
	return interactiveResult(state.waitErr, state.sessionErr)
}

func interactiveResult(waitErr, sessionErr error) (subprocess.Result, error) {
	result := subprocess.Result{}
	result.ExitCode = 0
	if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		result.ExitCode = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.Signal = status.Signal().String()
		}
	} else if waitErr != nil {
		result.ExitCode = -1
	}
	if sessionErr != nil {
		return result, sessionErr
	}
	return result, waitErr
}

type interactiveWait struct {
	waitDone   <-chan error
	outputDone <-chan error
	waitErr    error
	sessionErr error
	waited     bool
	drained    bool
}

func resizeInteractive(terminal pty.PTYCmd, a tunnel.TerminalAttachment, stop, done chan struct{}) {
	defer close(done)
	sizes := a.Resizes()
	for {
		select {
		case size, ok := <-sizes:
			if !ok {
				return
			}
			if size.Rows > 0 && size.Columns > 0 {
				_ = terminal.Resize(size.Rows, size.Columns)
			}
		case <-stop:
			return
		}
	}
}

func (s *interactiveWait) wait(ctx context.Context, a tunnel.TerminalAttachment) {
	for !s.waited || !s.drained {
		select {
		case s.waitErr = <-s.waitDone:
			s.waited = true
			s.waitDone = nil
		case err := <-s.outputDone:
			s.drained = true
			s.outputDone = nil
			s.sessionErr = err
		case <-ctx.Done():
			s.sessionErr = ctx.Err()
		case <-a.Canceled():
			s.sessionErr = context.Canceled
		}
		if s.sessionErr != nil {
			return
		}
	}
}

func (s *interactiveWait) stop(process pty.Process) {
	_ = syscall.Kill(-process.Pid(), syscall.SIGTERM)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for !s.waited || !s.drained {
		select {
		case s.waitErr = <-s.waitDone:
			s.waited = true
			s.waitDone = nil
		case <-s.outputDone:
			s.drained = true
			s.outputDone = nil
		case <-timer.C:
			_ = syscall.Kill(-process.Pid(), syscall.SIGKILL)
			return
		}
	}
}

func startInteractive(
	p hookRunParams,
	launch interactiveLaunch,
	a tunnel.TerminalAttachment,
) (pty.PTYCmd, pty.Process, error) {
	window := a.Window()
	cred := launch.identity.credentials
	env := os.Environ()
	if !cred.MatchCurrent {
		target, err := user.LookupId(strconv.FormatUint(uint64(cred.UID), 10))
		if err != nil {
			return nil, nil, fmt.Errorf("lookup execution user: %w", err)
		}
		shell, err := loginShell(target.Username)
		if err != nil {
			return nil, nil, err
		}
		env = append(env, "HOME="+target.HomeDir, "USER="+target.Username,
			"LOGNAME="+target.Username, "SHELL="+shell)
	}
	path, err := interactiveLookPath(launch.binary, p.env.workspaceFolder, p.env.remoteEnv)
	if err != nil {
		return nil, nil, err
	}
	cmd := pty.CommandContext(context.Background(), path, launch.args[1:]...)
	cmd.Dir = p.env.workspaceFolder
	cmd.Env = append(append(env, buildEnvArr(p.env.remoteEnv)...), "TERM="+window.Term)
	opts := []pty.Option{
		pty.WithSSHRequest(
			ssh.Pty{
				Term:   window.Term,
				Window: ssh.Window{Width: int(window.Columns), Height: int(window.Rows)},
			},
		),
	}
	if !cred.MatchCurrent {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: cred.UID, Gid: cred.GID, Groups: cred.Groups},
		}
		opts = append(opts, pty.WithTTYOwner(int(cred.UID), int(cred.GID)))
	}
	terminal, process, err := pty.Start(cmd, pty.WithPTYOption(opts...))
	if err != nil {
		return nil, nil, fmt.Errorf("start terminal command: %w", err)
	}
	return terminal, process, nil
}

func interactiveLookPath(binary, dir string, env map[string]string) (string, error) {
	if strings.Contains(binary, "/") {
		if !filepath.IsAbs(binary) {
			binary = filepath.Join(dir, binary)
		}
		return exec.LookPath(binary)
	}
	path, ok := env["PATH"]
	if !ok {
		path = os.Getenv("PATH")
	}
	for _, entry := range filepath.SplitList(path) {
		if !filepath.IsAbs(entry) {
			entry = filepath.Join(dir, entry)
		}
		candidate := filepath.Join(entry, binary)
		if found, err := exec.LookPath(candidate); err == nil {
			return found, nil
		}
	}
	return "", &exec.Error{Name: binary, Err: exec.ErrNotFound}
}
