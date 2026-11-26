package command

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cleanupTestJob = "devsy-test-job"

// exitingWorker models the race: the job is already gone and the worker is
// reported as running until its process object signals.
type exitingWorker struct {
	exitsOnWait bool
	running     bool
	waited      []time.Duration
	jobCalls    int
	probeErr    error
	waitErr     error
	terminated  bool
}

func (w *exitingWorker) probes() exitedJobProbes {
	return exitedJobProbes{
		terminateJob: func(name string) (bool, error) {
			w.jobCalls++
			return w.terminated, nil
		},
		workerRunning: func(ProcessRef) (bool, error) {
			return w.running, w.probeErr
		},
		waitForExit: func(_ ProcessRef, timeout time.Duration) error {
			w.waited = append(w.waited, timeout)
			if w.exitsOnWait {
				w.running = false
			}
			return w.waitErr
		},
	}
}

func cleanupTestRef() ProcessRef {
	return ProcessRef{PID: 4242, TreeKind: ProcessTreeWindowsJob, TreeID: cleanupTestJob}
}

func TestCleanupExitedJobWaitsForWorkerThatIsStillExiting(t *testing.T) {
	worker := &exitingWorker{running: true, exitsOnWait: true}

	err := cleanupExitedJob(cleanupTestRef(), worker.probes())

	require.NoError(t, err)
	assert.Equal(t, []time.Duration{workerExitGrace}, worker.waited)
}

func TestCleanupExitedJobKeepsErrorForWorkerThatOutlivesTheWait(t *testing.T) {
	worker := &exitingWorker{running: true}

	err := cleanupExitedJob(cleanupTestRef(), worker.probes())

	require.ErrorContains(t, err,
		"worker job "+cleanupTestJob+" is unavailable while process 4242 is running")
	assert.Equal(t, []time.Duration{workerExitGrace}, worker.waited)
}

func TestCleanupExitedJobDoesNotWaitWhenWorkerIsGone(t *testing.T) {
	worker := &exitingWorker{}

	require.NoError(t, cleanupExitedJob(cleanupTestRef(), worker.probes()))
	assert.Empty(t, worker.waited)
}

func TestCleanupExitedJobDoesNotWaitAfterTerminatingTheJob(t *testing.T) {
	worker := &exitingWorker{running: true, terminated: true}

	require.NoError(t, cleanupExitedJob(cleanupTestRef(), worker.probes()))
	assert.Empty(t, worker.waited)
	assert.Equal(t, 1, worker.jobCalls)
}

func TestCleanupExitedJobReturnsProbeAndWaitErrors(t *testing.T) {
	probeFailure := errors.New("identity lookup failed")
	waitFailure := errors.New("wait failed")
	tests := []struct {
		name   string
		worker *exitingWorker
		want   error
	}{
		{"probe", &exitingWorker{running: true, probeErr: probeFailure}, probeFailure},
		{"wait", &exitingWorker{running: true, waitErr: waitFailure}, waitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cleanupExitedJob(cleanupTestRef(), tt.worker.probes())

			require.ErrorIs(t, err, tt.want)
		})
	}
}
