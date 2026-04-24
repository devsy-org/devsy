package devcontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newInitCmdConfig(hook types.LifecycleHook) *config.DevContainerConfig {
	return &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			InitializeCommand: hook,
		},
	}
}

func TestRunInitializeCommand_ParallelTiming(t *testing.T) {
	// Two "sleep 0.5" commands that, if run in parallel, complete in ~0.5s.
	cfg := newInitCmdConfig(types.LifecycleHook{
		"sleep-a": {"sleep", "0.5"},
		"sleep-b": {"sleep", "0.5"},
	})

	start := time.Now()
	err := runInitializeCommand(t.TempDir(), cfg, nil)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, elapsed, 900*time.Millisecond,
		"two 0.5s commands should complete in ~0.5s when parallel, not ~1s")
}

func TestRunInitializeCommand_ParallelErrorCollection(t *testing.T) {
	dir := t.TempDir()
	markerFile := filepath.Join(dir, "ran.txt")

	cfg := newInitCmdConfig(types.LifecycleHook{
		"fail":    {"sh", "-c", "exit 1"},
		"succeed": {"sh", "-c", fmt.Sprintf("sleep 0.1 && echo done > %s", markerFile)},
	})

	err := runInitializeCommand(dir, cfg, nil)

	// The combined error should mention which named command failed.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `named command "fail" failed`)

	// The succeed command should have run to completion despite the failure.
	_, statErr := os.Stat(markerFile)
	assert.NoError(t, statErr, "succeed command should run even when fail command errors")
}

func TestRunInitializeCommand_SingleKey(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")

	cfg := newInitCmdConfig(types.LifecycleHook{
		"": {"sh", "-c", fmt.Sprintf("echo hello > %s", outFile)},
	})

	err := runInitializeCommand(dir, cfg, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(outFile) // #nosec G304 -- test path from t.TempDir
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
}

func TestRunInitializeCommand_StringFormat(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")

	// String format results in a single key with a shell command string.
	cfg := newInitCmdConfig(types.LifecycleHook{
		"": {fmt.Sprintf("echo hello > %s", outFile)},
	})

	err := runInitializeCommand(dir, cfg, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(outFile) // #nosec G304 -- test path from t.TempDir
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
}

func TestRunInitializeCommand_Empty(t *testing.T) {
	cfg := &config.DevContainerConfig{}
	err := runInitializeCommand(t.TempDir(), cfg, nil)
	assert.NoError(t, err)
}
