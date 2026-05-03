package command

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPathManager struct {
	config.PathManager
	runtimeDir string
}

func (t *testPathManager) RuntimeDir() (string, error) { return t.runtimeDir, nil }

func TestStartBackgroundRunsEveryTime(t *testing.T) {
	tmpDir := t.TempDir()
	config.SetPathManager(
		&testPathManager{PathManager: config.NewPathManager(), runtimeDir: tmpDir},
	)
	t.Cleanup(func() { config.ResetPathManager() })

	commandName := fmt.Sprintf("test.start-bg.%d", time.Now().UnixNano())

	callCount := 0
	createCmd := func() (*exec.Cmd, error) {
		callCount++
		return exec.Command("true"), nil
	}

	err := StartBackground(commandName, createCmd)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	err = StartBackground(commandName, createCmd)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount, "StartBackground must invoke createCommand on every call")
}

func TestStartBackgroundOnceSkipsSecondCall(t *testing.T) {
	tmpDir := t.TempDir()
	config.SetPathManager(
		&testPathManager{PathManager: config.NewPathManager(), runtimeDir: tmpDir},
	)
	t.Cleanup(func() { config.ResetPathManager() })

	commandName := fmt.Sprintf("test.start-bg-once.%d", time.Now().UnixNano())

	callCount := 0
	createCmd := func() (*exec.Cmd, error) {
		callCount++
		return exec.Command("sleep", "5"), nil
	}

	err := StartBackgroundOnce(commandName, createCmd)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	err = StartBackgroundOnce(commandName, createCmd)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "StartBackgroundOnce must skip when process is already running")
}
