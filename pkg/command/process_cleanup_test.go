package command

import (
	"context"
	"errors"
	"math"
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
	onWait      func()
}

func (w *exitingWorker) probes() exitedJobProbes {
	return exitedJobProbes{
		terminateJob: func(string) (bool, error) {
			w.jobCalls++
			return w.terminated, nil
		},
		workerRunning: func(ProcessRef) (bool, error) {
			return w.running, w.probeErr
		},
		waitForExit: func(_ context.Context, _ ProcessRef, timeout time.Duration) error {
			w.waited = append(w.waited, timeout)
			if w.onWait != nil {
				w.onWait()
			}
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

	err := cleanupExitedJob(context.Background(), cleanupTestRef(), worker.probes())

	require.NoError(t, err)
	assert.Equal(t, []time.Duration{5 * time.Second}, worker.waited)
}

func TestCleanupExitedJobKeepsErrorForWorkerThatOutlivesTheWait(t *testing.T) {
	worker := &exitingWorker{running: true}

	err := cleanupExitedJob(context.Background(), cleanupTestRef(), worker.probes())

	require.ErrorContains(t, err,
		"worker job "+cleanupTestJob+" is unavailable while process 4242 is running")
	assert.Equal(t, []time.Duration{5 * time.Second}, worker.waited)
}

func TestCleanupExitedJobDoesNotWaitWhenWorkerIsGone(t *testing.T) {
	worker := &exitingWorker{}

	require.NoError(t, cleanupExitedJob(context.Background(), cleanupTestRef(), worker.probes()))
	assert.Empty(t, worker.waited)
}

func TestCleanupExitedJobDoesNotWaitAfterTerminatingTheJob(t *testing.T) {
	worker := &exitingWorker{running: true, terminated: true}

	require.NoError(t, cleanupExitedJob(context.Background(), cleanupTestRef(), worker.probes()))
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
			err := cleanupExitedJob(context.Background(), cleanupTestRef(), tt.worker.probes())

			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestCleanupExitedJobShortensTheWaitToTheContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	worker := &exitingWorker{running: true, exitsOnWait: true}

	require.NoError(t, cleanupExitedJob(ctx, cleanupTestRef(), worker.probes()))

	require.Len(t, worker.waited, 1)
	assert.Positive(t, worker.waited[0])
	assert.LessOrEqual(t, worker.waited[0], 2*time.Second)
}

func TestCleanupExitedJobKeepsTheGraceWhenTheDeadlineIsLater(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	worker := &exitingWorker{running: true, exitsOnWait: true}

	require.NoError(t, cleanupExitedJob(ctx, cleanupTestRef(), worker.probes()))

	assert.Equal(t, []time.Duration{5 * time.Second}, worker.waited)
}

func TestCleanupExitedJobReturnsTheContextErrorWithoutWaiting(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"cancelled", cancelled, context.Canceled},
		{"past deadline", expired, context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker := &exitingWorker{running: true, exitsOnWait: true}

			err := cleanupExitedJob(tt.ctx, cleanupTestRef(), worker.probes())

			require.ErrorIs(t, err, tt.want)
			assert.Empty(t, worker.waited)
		})
	}
}

func TestCleanupExitedJobReturnsTheContextErrorWhenItEndsDuringTheWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &exitingWorker{running: true, onWait: cancel}

	err := cleanupExitedJob(ctx, cleanupTestRef(), worker.probes())

	require.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "unavailable while process")
}

func TestWaitSliceMillisIsBounded(t *testing.T) {
	tests := []struct {
		name      string
		remaining time.Duration
		want      uint32
	}{
		{"negative", -time.Second, 0},
		{"zero", 0, 0},
		{"sub millisecond rounds up", time.Nanosecond, 1},
		{"short", 50 * time.Millisecond, 50},
		{"slice limit", 100 * time.Millisecond, 100},
		{"grace period", workerExitGrace, 100},
		{"longest duration", time.Duration(math.MaxInt64), 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := waitSliceMillis(tt.remaining)

			assert.Equal(t, tt.want, got)
			assert.NotEqual(t, uint32(math.MaxUint32), got, "must never be INFINITE")
		})
	}
}
