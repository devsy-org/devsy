package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type CommandSpec struct {
	Path    string
	Args    []string
	Timeout time.Duration
	Reserve time.Duration
}

type CommandResult struct {
	TimedOut  bool
	ExitCode  *int
	ProcessID int
	Stdout    string
	Stderr    string
}

func (r CommandResult) Output() string { return r.Stdout + r.Stderr }

func (r CommandResult) Success() bool { return !r.TimedOut && r.ExitCode != nil && *r.ExitCode == 0 }

func exitCodeString(code *int) string {
	if code == nil {
		return "none"
	}
	return fmt.Sprintf("%d", *code)
}

type Runner interface {
	Run(context.Context, CommandSpec) CommandResult
}

type ExecRunner struct{ Log *Logger }

func (r *ExecRunner) Run(ctx context.Context, spec CommandSpec) CommandResult {
	if spec.Timeout <= 0 {
		return CommandResult{TimedOut: true, Stderr: "shared bootstrap budget exhausted"}
	}
	capture, err := newCommandCapture()
	if err != nil {
		return commandError(spec, err)
	}
	defer capture.Close()

	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Stdout, cmd.Stderr = capture.stdout, capture.stderr
	if err := cmd.Start(); err != nil {
		return commandError(spec, err)
	}
	pid := cmd.Process.Pid
	waitResult := waitForProcess(ctx, cmd, spec.Timeout)
	out, stderr := capture.Read()
	result := CommandResult{
		TimedOut:  waitResult.timedOut,
		ProcessID: pid,
		Stdout:    out,
		Stderr:    stderr,
	}
	if waitResult.timedOut {
		result.Stderr += fmt.Sprintf(
			"\nPODMAN_COMMAND_TIMEOUT: direct_process_stopped=%t",
			waitResult.stopped,
		)
		if !waitResult.stopped {
			result.Stderr += "\nPODMAN_WINDOWS_TERMINATION_FAILED"
		}
		return result
	}
	code := 0
	if waitResult.err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](waitResult.err); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
			result.Stderr += "\n" + waitResult.err.Error()
		}
	}
	result.ExitCode = &code
	return result
}

type commandCapture struct {
	stdout *os.File
	stderr *os.File
}

func newCommandCapture() (*commandCapture, error) {
	stdout, err := os.CreateTemp("", "devsy-podman-stdout-*")
	if err != nil {
		return nil, err
	}
	stderr, err := os.CreateTemp("", "devsy-podman-stderr-*")
	if err != nil {
		_ = stdout.Close()
		_ = os.Remove(stdout.Name())
		return nil, err
	}
	return &commandCapture{stdout: stdout, stderr: stderr}, nil
}

func (c *commandCapture) Read() (string, string) {
	_ = c.stdout.Sync()
	_ = c.stderr.Sync()
	stdout, _ := os.ReadFile(c.stdout.Name())
	stderr, _ := os.ReadFile(c.stderr.Name())
	return string(stdout), string(stderr)
}

func (c *commandCapture) Close() {
	stdoutName, stderrName := c.stdout.Name(), c.stderr.Name()
	_ = c.stdout.Close()
	_ = c.stderr.Close()
	_ = os.Remove(stdoutName)
	_ = os.Remove(stderrName)
}

type processWaitResult struct {
	err      error
	timedOut bool
	stopped  bool
}

func waitForProcess(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) processWaitResult {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline); left < timeout {
			timeout = nonNegativeDuration(left)
		}
	}
	started := time.Now()
	terminationReserve := minRunnerDuration(2*time.Second, timeout/5)
	commandWait := timeout - terminationReserve
	timer := time.NewTimer(commandWait)
	defer timer.Stop()
	select {
	case err := <-done:
		return processWaitResult{err: err, stopped: true}
	case <-ctx.Done():
		return terminateProcess(cmd, done, nonNegativeDuration(timeout-time.Since(started)))
	case <-timer.C:
		return terminateProcess(cmd, done, nonNegativeDuration(timeout-time.Since(started)))
	}
}

func terminateProcess(cmd *exec.Cmd, done <-chan error, grace time.Duration) processWaitResult {
	stopped := true
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		stopped = false
	}
	grace = minRunnerDuration(2*time.Second, grace)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return processWaitResult{err: err, timedOut: true, stopped: stopped}
	case <-timer.C:
		return processWaitResult{timedOut: true, stopped: false}
	}
}

func minRunnerDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func commandError(spec CommandSpec, err error) CommandResult {
	code := -1
	return CommandResult{
		ExitCode: &code,
		Stderr:   fmt.Sprintf("%s %s: %v", spec.Path, strings.Join(spec.Args, " "), err),
	}
}

func nonNegativeDuration(duration time.Duration) time.Duration {
	if duration < 0 {
		return 0
	}
	return duration
}
