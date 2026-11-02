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
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
		if err != nil {
			_, _ = fmt.Fprintln(hc.Stderr, err)
			return interp.ExitStatus(127)
		}

		err = runOwnedProviderExec(ctx, hc, path, args)
		if errors.Is(err, syscall.ENOEXEC) && runtime.GOOS != "windows" {
			err = runOwnedProviderExec(
				ctx,
				hc,
				"/bin/sh",
				append([]string{"/bin/sh", path}, args[1:]...),
			)
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
		_, _ = fmt.Fprintln(stderr, err)
		return interp.ExitStatus(127)
	}
	return err
}

func runOwnedProviderExec(
	ctx context.Context,
	hc interp.HandlerContext,
	path string,
	args []string,
) error {
	cmd, err := startProviderExec(ctx, hc, path, args)
	if err != nil {
		return err
	}
	if ctx.Done() == nil {
		return cmd.Wait()
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
	return err
}

func startProviderExec(
	ctx context.Context,
	hc interp.HandlerContext,
	path string,
	args []string,
) (*exec.Cmd, error) {
	environ := providerExecEnv(hc.Env)
	newCommand := func() *exec.Cmd {
		return &exec.Cmd{
			Path:      path,
			Args:      args,
			Env:       environ,
			Dir:       hc.Dir,
			Stdin:     hc.Stdin,
			Stdout:    hc.Stdout,
			Stderr:    hc.Stderr,
			WaitDelay: 2 * time.Second,
		}
	}
	cmd := newCommand()
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	err := cmd.Start()
	for delay := time.Millisecond; errors.Is(err, syscall.ETXTBSY) && delay < 300*time.Millisecond; delay *= 2 {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(delay):
		}
		cmd = newCommand()
		err = cmd.Start()
	}
	if err != nil {
		return nil, err
	}
	return cmd, nil
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
