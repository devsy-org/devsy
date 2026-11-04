package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

func ProviderExecHandler() interp.ExecHandlerFunc {
	return providerExecHandler(nil)
}

func providerExecHandler(supervisor *providerProcessSupervisor) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
		if err != nil {
			_, _ = fmt.Fprintln(hc.Stderr, "provider executable not found")
			return interp.ExitStatus(127)
		}

		command := providerExecCommand{path: path, args: args}
		err = runOwnedProviderExec(ctx, hc, command, supervisor)
		if errors.Is(err, syscall.ENOEXEC) && runtime.GOOS != providerWindowsOS {
			command.path = "/bin/sh"
			command.args = append([]string{"/bin/sh", path}, args[1:]...)
			err = runOwnedProviderExec(ctx, hc, command, supervisor)
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return providerExecResult(err, hc.Stderr)
	}
}

func providerExecResult(err error, stderr io.Writer) error {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if status, ok := providerExecSignalStatus(exitErr); ok {
			return status
		}
		code := exitErr.ExitCode()
		if code < 0 || code > 255 {
			return fmt.Errorf("provider process exit code outside shell range: %d", code)
		}
		return interp.ExitStatus(uint8(code))
	}
	if _, ok := errors.AsType[*exec.Error](err); ok {
		_, _ = fmt.Fprintln(stderr, "provider executable could not start")
		return interp.ExitStatus(127)
	}
	return err
}

type providerExecCommand struct {
	path string
	args []string
}

func runOwnedProviderExec(
	ctx context.Context,
	hc interp.HandlerContext,
	command providerExecCommand,
	supervisor *providerProcessSupervisor,
) error {
	cmd, err := startProviderExec(ctx, hc, command, supervisor)
	if err != nil {
		return err
	}
	if ctx.Done() == nil {
		err = cmd.Wait()
		if supervisor != nil {
			supervisor.reaped(cmd)
		}
		return err
	}
	done := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	<-monitorDone
	if supervisor != nil {
		supervisor.reaped(cmd)
	}
	return err
}

func startProviderExec(
	ctx context.Context,
	hc interp.HandlerContext,
	command providerExecCommand,
	supervisor *providerProcessSupervisor,
) (*exec.Cmd, error) {
	environ := providerExecEnv(hc.Env)
	newCommand := func() *exec.Cmd {
		return &exec.Cmd{
			Path:      command.path,
			Args:      command.args,
			Env:       environ,
			Dir:       hc.Dir,
			Stdin:     hc.Stdin,
			Stdout:    hc.Stdout,
			Stderr:    hc.Stderr,
			WaitDelay: 2 * time.Second,
		}
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if supervisor != nil {
		return supervisor.start(ctx, newCommand)
	}
	return startProviderExecRetry(ctx, newCommand)
}

func providerExecEnv(env expand.Environ) []string {
	vars := make([]string, 0, 64)
	for name, variable := range env.Each {
		if !variable.IsSet() {
			for i, kv := range vars {
				if strings.HasPrefix(kv, name+"=") {
					vars[i] = ""
				}
			}
		}
		if variable.Exported && variable.Kind == expand.String {
			vars = append(vars, name+"="+variable.String())
		}
	}
	return vars
}
