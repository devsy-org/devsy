package command

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartBackgroundRunsEveryTime(t *testing.T) {
	callCount := 0
	createCmd := func() (*exec.Cmd, error) {
		callCount++
		return exec.Command("true"), nil
	}

	err := StartBackground("test.start-bg", createCmd)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	err = StartBackground("test.start-bg", createCmd)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount, "StartBackground must invoke createCommand on every call")
}

func TestStartBackgroundOnceSkipsSecondCall(t *testing.T) {
	callCount := 0
	createCmd := func() (*exec.Cmd, error) {
		callCount++
		// Use sleep so the process is still running when the second call happens.
		return exec.Command("sleep", "5"), nil
	}

	err := StartBackgroundOnce("test.start-bg-once", createCmd)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	err = StartBackgroundOnce("test.start-bg-once", createCmd)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "StartBackgroundOnce must skip when process is already running")
}
