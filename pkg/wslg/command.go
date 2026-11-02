package wslg

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, path string, args []string) (ProbeResult, error) {
	//nolint:gosec // System API resolves the executable; argv is a fixed printenv probe.
	cmd := exec.CommandContext(ctx, path, args...)
	// WSLENV would translate Windows variables such as DISPLAY into the probe.
	cmd.Env = withoutWSLENV(os.Environ())
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

func withoutWSLENV(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "WSLENV") {
			out = append(out, entry)
		}
	}
	return out
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
