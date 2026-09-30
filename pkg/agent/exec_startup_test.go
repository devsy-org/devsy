package agent

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func blockingExec(ctx context.Context, _ ExecRequest) error {
	<-ctx.Done()
	return ctx.Err()
}

func watchdogOp() ExecRequest {
	return ExecRequest{User: "root", Command: "cmd", Stdout: io.Discard, Stderr: io.Discard}
}

func TestExecWithStartupWatchdog_SilentExecIsWedged(t *testing.T) {
	start := time.Now()
	err := ExecWithStartupWatchdog(
		context.Background(), blockingExec, watchdogOp(),
		ExecStartupWatchdogOptions{Timeout: 50 * time.Millisecond},
	)

	var silenceErr *ExecStartupSilenceError
	require.ErrorAs(t, err, &silenceErr)
	assert.True(t, silenceErr.Timeout())
	assert.True(t, silenceErr.Temporary())
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestExecWithStartupWatchdog_OutputDisarmsWatchdog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wrote := make(chan struct{})
	exec := func(ctx context.Context, req ExecRequest) error {
		_, _ = req.Stdout.Write([]byte("SSH-2.0-devsy\r\n"))
		close(wrote)
		<-ctx.Done()
		return ctx.Err()
	}

	go func() {
		<-wrote
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	err := ExecWithStartupWatchdog(
		ctx, exec, watchdogOp(), ExecStartupWatchdogOptions{Timeout: 50 * time.Millisecond},
	)
	require.ErrorIs(t, err, context.Canceled)
	var silenceErr *ExecStartupSilenceError
	assert.NotErrorAs(t, err, &silenceErr)
}

func TestExecWithStartupWatchdog_OutputThenCancellationIsPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	exec := func(_ context.Context, req ExecRequest) error {
		_, _ = req.Stdout.Write([]byte("SSH-2.0-devsy\r\n"))
		close(started)
		select {}
	}

	result := make(chan error, 1)
	go func() {
		result <- ExecWithStartupWatchdog(ctx, exec, watchdogOp(), ExecStartupWatchdogOptions{Timeout: 50 * time.Millisecond})
	}()
	<-started
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("watchdog did not observe cancellation after startup")
	}
}

func TestExecWithStartupWatchdog_PropagatesExecError(t *testing.T) {
	sentinel := errors.New("exec failed")
	exec := func(context.Context, ExecRequest) error {
		return sentinel
	}

	err := ExecWithStartupWatchdog(
		context.Background(), exec, watchdogOp(), ExecStartupWatchdogOptions{Timeout: time.Hour},
	)
	require.ErrorIs(t, err, sentinel)
}

func TestExecWithStartupWatchdog_ParentCancelIsNotWedged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := ExecWithStartupWatchdog(
		ctx, blockingExec, watchdogOp(), ExecStartupWatchdogOptions{Timeout: time.Hour},
	)
	require.ErrorIs(t, err, context.Canceled)
	var silenceErr *ExecStartupSilenceError
	assert.NotErrorAs(t, err, &silenceErr)
}

func TestExecWithStartupWatchdog_SilenceWaitsForExecTermination(t *testing.T) {
	terminated := make(chan struct{})
	exec := func(ctx context.Context, _ ExecRequest) error {
		<-ctx.Done()
		close(terminated)
		return ctx.Err()
	}

	opts := ExecStartupWatchdogOptions{Timeout: 50 * time.Millisecond}
	err := ExecWithStartupWatchdog(
		context.Background(),
		exec,
		watchdogOp(),
		opts,
	)

	var silenceErr *ExecStartupSilenceError
	require.ErrorAs(t, err, &silenceErr)
	select {
	case <-terminated:
	default:
		t.Fatal("startup silence error returned before exec termination")
	}
}
