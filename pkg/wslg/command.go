package wslg

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, path string, args []string) (ProbeResult, error) {
	//nolint:gosec // System API resolves the executable; argv is a fixed printenv probe.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	stdout, stderr := &boundedOutput{}, &boundedOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return ProbeResult{}, ctx.Err()
	}
	result := ProbeResult{Stdout: stdout.data, Stderr: stderr.data, ExitCode: -1}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if stdout.overflow || stderr.overflow {
		return ProbeResult{}, ErrProtocol
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return result, nil
	}
	return result, err
}

type boundedOutput struct {
	data     []byte
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	remaining := outputLimit - len(b.data)
	if len(p) > remaining {
		b.overflow = true
	}
	b.data = append(b.data, p[:min(len(p), remaining)]...)
	return len(p), nil
}
