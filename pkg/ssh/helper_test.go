package ssh

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestExitError_ErrorIncludesErrWhenSet(t *testing.T) {
	inner := errors.New("permission denied")
	err := &ExitError{ExitCode: 1, Err: inner}

	assert.Equal(t, "exit status 1: permission denied", err.Error())
}

func TestExitError_ErrorOmitsErrWhenNil(t *testing.T) {
	err := &ExitError{ExitCode: 127}

	assert.Equal(t, "exit status 127", err.Error())
}

func TestExitError_UnwrapReturnsWrappedErr(t *testing.T) {
	inner := errors.New("boom")
	err := &ExitError{ExitCode: 2, Err: inner}

	require.ErrorIs(t, err, inner)
	assert.Same(t, inner, errors.Unwrap(err))
}

func TestRunOptions_ValidateRequiresClient(t *testing.T) {
	err := (&RunOptions{Command: "ls"}).validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH client is required")
}

func TestRunOptions_ValidateRequiresCommand(t *testing.T) {
	err := (&RunOptions{Client: &ssh.Client{}}).validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestRunOptions_ValidatePassesForClientAndCommand(t *testing.T) {
	err := (&RunOptions{Client: &ssh.Client{}, Command: "ls"}).validate()

	assert.NoError(t, err)
}

func TestIsSignalInterrupt(t *testing.T) {
	signalExits := []int{130, 129, 143}
	for _, code := range signalExits {
		assert.True(t, isSignalInterrupt(code), "exit code %d should be a signal interrupt", code)
	}

	nonSignalExits := []int{0, 1, 2, 127, 128, 131, 142, 144, 255, -1}
	for _, code := range nonSignalExits {
		assert.False(
			t,
			isSignalInterrupt(code),
			"exit code %d should not be a signal interrupt",
			code,
		)
	}
}

func TestHandleRunError_CancelledContextReturnsContextErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := handleRunError(ctx, io.EOF, "cmd")

	require.ErrorIs(t, err, context.Canceled)
}

func TestHandleRunError_EOFIsWrappedWithCommand(t *testing.T) {
	err := handleRunError(context.Background(), io.EOF, "build")

	require.ErrorIs(t, err, io.EOF)
	assert.Contains(t, err.Error(), "SSH session closed unexpectedly while running build")
}

func TestHandleRunError_GenericErrorIsWrappedWithCommand(t *testing.T) {
	inner := errors.New("connection reset")
	err := handleRunError(context.Background(), inner, "test")

	require.ErrorIs(t, err, inner)
	assert.Contains(t, err.Error(), "SSH command failed while running test")
}

func TestHandleRunError_ExitErrorIsWrappedInDevsyExitError(t *testing.T) {
	sshExitErr := &ssh.ExitError{}

	err := handleRunError(context.Background(), sshExitErr, "run")

	var devsyExit *ExitError
	require.ErrorAs(t, err, &devsyExit)
	assert.Equal(t, 0, devsyExit.ExitCode)
	require.ErrorIs(t, err, sshExitErr)
}

func TestSetupContextCancellation_AlreadyCancelledReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cleanup, err := setupContextCancellation(ctx, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, cleanup)
}

func TestSetupContextCancellation_ReturnsCleanupThatStopsWatcher(t *testing.T) {
	cleanup, err := setupContextCancellation(context.Background(), nil)

	require.NoError(t, err)
	require.NotNil(t, cleanup)

	assert.NotPanics(t, cleanup)
}
