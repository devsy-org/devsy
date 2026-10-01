package agent

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

const (
	defaultExecStartupSilenceTimeout  = 30 * time.Second
	defaultExecTerminationWaitTimeout = 5 * time.Second
)

type ExecStartupWatchdogOptions struct {
	Timeout time.Duration
	// TerminationWaitTimeout bounds the total wait for OnStartupSilence and the
	// exec callback to return after cancellation. It does not guarantee either
	// has stopped when the watchdog returns.
	TerminationWaitTimeout time.Duration
	OnStartupSilence       func()
}

type ExecStartupSilenceError struct {
	timeout time.Duration
}

func (e *ExecStartupSilenceError) Error() string {
	return fmt.Sprintf(
		"container exec session produced no output for %s: remote process never started",
		e.timeout,
	)
}

func (e *ExecStartupSilenceError) Timeout() bool   { return true }
func (e *ExecStartupSilenceError) Temporary() bool { return true }

type startupActivityWriter struct {
	w      io.Writer
	active *atomic.Bool
}

func (s *startupActivityWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		s.active.Store(true)
	}
	return s.w.Write(p)
}

func ExecWithStartupWatchdog(
	ctx context.Context,
	exec Exec,
	req ExecRequest,
	opts ExecStartupWatchdogOptions,
) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultExecStartupSilenceTimeout
	}
	terminationWaitTimeout := opts.TerminationWaitTimeout
	if terminationWaitTimeout <= 0 {
		terminationWaitTimeout = defaultExecTerminationWaitTimeout
	}
	return execWithStartupWatchdog(ctx, exec, req, ExecStartupWatchdogOptions{
		Timeout:                timeout,
		TerminationWaitTimeout: terminationWaitTimeout,
		OnStartupSilence:       opts.OnStartupSilence,
	})
}

func execWithStartupWatchdog(
	ctx context.Context,
	exec Exec,
	req ExecRequest,
	opts ExecStartupWatchdogOptions,
) error {
	if req.Stdout == nil {
		req.Stdout = io.Discard
	}
	if req.Stderr == nil {
		req.Stderr = io.Discard
	}

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	active := &atomic.Bool{}
	execDone := make(chan error, 1)
	go func() {
		req.Stdout = &startupActivityWriter{w: req.Stdout, active: active}
		req.Stderr = &startupActivityWriter{w: req.Stderr, active: active}
		execDone <- exec(watchCtx, req)
	}()

	timer := time.NewTimer(opts.Timeout)
	defer timer.Stop()
	timerC := timer.C

	for {
		select {
		case err := <-execDone:
			return err
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-timerC:
			if active.Load() {
				// Startup completed. Keep observing cancellation while the
				// long-lived SSH server remains running.
				timerC = nil
				continue
			}
			stopExecAfterStartupSilence(cancel, execDone, opts)
			return &ExecStartupSilenceError{timeout: opts.Timeout}
		}
	}
}

func stopExecAfterStartupSilence(
	cancel context.CancelFunc,
	execDone <-chan error,
	opts ExecStartupWatchdogOptions,
) {
	cancel()
	waitTimer := time.NewTimer(opts.TerminationWaitTimeout)
	defer waitTimer.Stop()
	if opts.OnStartupSilence != nil {
		interruptDone := make(chan struct{})
		go func() {
			opts.OnStartupSilence()
			close(interruptDone)
		}()
		select {
		case <-interruptDone:
		case <-waitTimer.C:
			return
		}
	}
	select {
	case <-execDone:
	case <-waitTimer.C:
	}
}
