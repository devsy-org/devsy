package command

import (
	"context"
	"fmt"
	"time"
)

// workerExitGrace bounds how long cleanup waits for a worker whose job has
// already disappeared. Windows removes a job when its last process exits, and
// the process object can signal a moment later than the job vanishes.
const workerExitGrace = 5 * time.Second

// maxWaitSlice is the longest single blocking wait on a process handle. A
// short slice lets the wait notice a cancelled context and keeps the count far
// from the INFINITE sentinel (0xFFFFFFFF).
const maxWaitSlice = 100 * time.Millisecond

// exitedJobProbes holds the system calls cleanupExitedJob makes, so a test can
// order them.
type exitedJobProbes struct {
	terminateJob  func(name string) (bool, error)
	workerRunning func(ref ProcessRef) (bool, error)
	waitForExit   func(ctx context.Context, ref ProcessRef, timeout time.Duration) error
}

// cleanupExitedJob finishes a worker tree that is expected to be gone. A
// missing job with a worker still reported as running is an error only when
// the worker outlives the wait, which ends at the grace period or the context
// deadline, whichever comes first.
func cleanupExitedJob(ctx context.Context, ref ProcessRef, probes exitedJobProbes) error {
	terminated, err := probes.terminateJob(ref.TreeID)
	if err != nil || terminated {
		return err
	}
	running, err := probes.workerRunning(ref)
	if err != nil || !running {
		return err
	}
	if err := waitForWorkerToExit(ctx, ref, probes); err != nil {
		return err
	}
	return nil
}

// waitForWorkerToExit waits out the grace period and fails only when the
// worker is still running afterwards and the context has not ended.
func waitForWorkerToExit(ctx context.Context, ref ProcessRef, probes exitedJobProbes) error {
	wait, err := exitWait(ctx)
	if err != nil {
		return err
	}
	if err := probes.waitForExit(ctx, ref, wait); err != nil {
		return err
	}
	running, err := probes.workerRunning(ref)
	if err != nil || !running {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf(
		"worker job %s is unavailable while process %d is running",
		ref.TreeID,
		ref.PID,
	)
}

// exitWait returns the grace period shortened to the time left before the
// context deadline, or the context error when nothing is left.
func exitWait(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	wait := workerExitGrace
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline))
	}
	if wait <= 0 {
		return 0, context.DeadlineExceeded
	}
	return wait, nil
}

// waitSliceMillis converts the time left into one bounded blocking wait of at
// most maxWaitSlice, rounded up so a sub-millisecond remainder does not spin.
func waitSliceMillis(remaining time.Duration) uint32 {
	slice := min(max(remaining, 0), maxWaitSlice)
	return uint32((slice + time.Millisecond - 1) / time.Millisecond)
}
