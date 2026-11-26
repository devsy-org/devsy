package command

import (
	"fmt"
	"time"
)

// workerExitGrace bounds how long cleanup waits for a worker whose job has
// already disappeared. Windows removes a job when its last process exits, and
// the process object can signal a moment later than the job vanishes.
const workerExitGrace = 5 * time.Second

// exitedJobProbes holds the system calls cleanupExitedJob makes, so a test can
// order them.
type exitedJobProbes struct {
	terminateJob  func(name string) (bool, error)
	workerRunning func(ref ProcessRef) (bool, error)
	waitForExit   func(ref ProcessRef, timeout time.Duration) error
}

// cleanupExitedJob finishes a worker tree that is expected to be gone. A
// missing job with a worker still reported as running is an error only when
// the worker outlives the grace period.
func cleanupExitedJob(ref ProcessRef, probes exitedJobProbes) error {
	terminated, err := probes.terminateJob(ref.TreeID)
	if err != nil || terminated {
		return err
	}
	running, err := probes.workerRunning(ref)
	if err != nil || !running {
		return err
	}
	if err := probes.waitForExit(ref, workerExitGrace); err != nil {
		return err
	}
	running, err = probes.workerRunning(ref)
	if err != nil || !running {
		return err
	}
	return fmt.Errorf(
		"worker job %s is unavailable while process %d is running",
		ref.TreeID,
		ref.PID,
	)
}
