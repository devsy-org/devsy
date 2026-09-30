package agent

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

const defaultExecStartupSilenceTimeout = 30 * time.Second

type ExecStartupWatchdogOptions struct {
	Timeout time.Duration
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
	return execWithStartupWatchdog(ctx, exec, req, timeout)
}

func execWithStartupWatchdog(
	ctx context.Context,
	exec Exec,
	req ExecRequest,
	timeout time.Duration,
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

	timer := time.NewTimer(timeout)
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
			cancel()
			<-execDone
			return &ExecStartupSilenceError{timeout: timeout}
		}
	}
}
