package shell

import (
	"context"
	"errors"
	"fmt"
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
			fmt.Fprintln(hc.Stderr, err)
			return interp.ExitStatus(127)
		}

		newCommand := func(path string, args []string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, path, args[1:]...)
			cmd.Args = args
			cmd.Env = providerExecEnv(hc.Env)
			cmd.Dir = hc.Dir
			cmd.Stdin = hc.Stdin
			cmd.Stdout = hc.Stdout
			cmd.Stderr = hc.Stderr
			cmd.WaitDelay = 2 * time.Second
			return cmd
		}

		cmd := newCommand(path, args)
		err = cmd.Run()
		if errors.Is(err, syscall.ENOEXEC) && runtime.GOOS != "windows" {
			cmd = newCommand("/bin/sh", append([]string{"/bin/sh", path}, args[1:]...))
			err = cmd.Run()
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := providerExecSignalStatus(exitErr); ok {
				return status
			}
			return interp.ExitStatus(exitErr.ExitCode())
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			fmt.Fprintln(hc.Stderr, err)
			return interp.ExitStatus(127)
		}
		return err
	}
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
