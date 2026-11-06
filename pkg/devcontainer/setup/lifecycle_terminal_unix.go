//go:build unix

package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/pty"
	"github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/devsy-org/ssh"
)

type interactiveIdentity struct{ credentials server.ExecCredentials }

func loginShell(name string) (string, error) {
	lookup, _, _ := strings.Cut(name, ":")
	if out, err := exec.Command("getent", "passwd", lookup).Output(); err == nil {
		if shell := shellFromPasswd(string(out), lookup); shell != "" {
			return shell, nil
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
	window := a.Window()
	path, err := exec.LookPath(launch.binary)
	if err != nil {
		return result, err
	}
	cmd := pty.CommandContext(context.Background(), path, launch.args[1:]...)
	cmd.Dir = p.env.workspaceFolder
	cmd.Env = append(append(os.Environ(), buildEnvArr(p.env.remoteEnv)...), "TERM="+window.Term)
	opts := []pty.Option{
		pty.WithSSHRequest(
			ssh.Pty{
				Term:   window.Term,
				Window: ssh.Window{Width: int(window.Columns), Height: int(window.Rows)},
			},
		),
	}
	cred := launch.identity.credentials
	if !cred.MatchCurrent {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: cred.UID, Gid: cred.GID, Groups: cred.Groups},
		}
		opts = append(opts, pty.WithTTYOwner(int(cred.UID), int(cred.GID)))
	}
	terminal, process, err := pty.Start(cmd, pty.WithPTYOption(opts...))
	if err != nil {
		return result, fmt.Errorf("start terminal command: %w", err)
	}
	inputDone := make(chan struct{})
	outputDone := make(chan error, 1)
	waitDone := make(chan error, 1)
	resizeDone := make(chan struct{})
	stopResize := make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(terminal.InputWriter(), input) }()
	go func() { _, copyErr := io.Copy(a.Output(), terminal.OutputReader()); outputDone <- copyErr }()
	go func() { waitDone <- process.Wait() }()
	go func() {
		defer close(resizeDone)
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
			case <-stopResize:
				return
			}
		}
	}()
	var waitErr, outputErr, sessionErr error
	waited, drained := false, false
	for !waited || !drained {
		select {
		case waitErr = <-waitDone:
			releaseDarwinTTY(terminal)
			waited = true
			waitDone = nil
		case outputErr = <-outputDone:
			drained = true
			outputDone = nil
			if outputErr != nil {
				sessionErr = outputErr
			}
		case <-ctx.Done():
			sessionErr = ctx.Err()
		case <-a.Canceled():
			sessionErr = context.Canceled
		}
		if sessionErr != nil {
			break
		}
	}
	if sessionErr != nil {
		_ = syscall.Kill(-process.Pid(), syscall.SIGTERM)
		timer := time.NewTimer(2 * time.Second)
	grace:
		for !waited || !drained {
			select {
			case waitErr = <-waitDone:
				releaseDarwinTTY(terminal)
				waited = true
				waitDone = nil
			case outputErr = <-outputDone:
				drained = true
				outputDone = nil
			case <-timer.C:
				_ = syscall.Kill(-process.Pid(), syscall.SIGKILL)
				break grace
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	close(stopResize)
	<-resizeDone
	_ = terminal.Close()
	if !waited {
		waitErr = <-waitDone
	}
	if !drained {
		<-outputDone
	}
	_ = input.Close()
	<-inputDone
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

func releaseDarwinTTY(terminal pty.PTYCmd) {
	if runtime.GOOS != "darwin" {
		return
	}
	if pair, ok := terminal.(interface{ Output() pty.ReadWriter }); ok {
		if closer, ok := pair.Output().Writer.(io.Closer); ok {
			_ = closer.Close()
		}
	}
}
