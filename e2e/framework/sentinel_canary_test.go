package framework

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSentinelCanaryRegisteredAtStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinels")
	t.Setenv("DEVSY_E2E_SENTINEL_FILE", path)
	t.Setenv("DEVSY_CAPTURE_CHILD", "exit")
	executable, err := os.Executable()
	require.NoError(t, err)
	f := &Framework{DevsyBinDir: filepath.Dir(executable), DevsyBinName: filepath.Base(executable)}
	_, _, _ = f.ExecCommandCapture(context.Background(), []string{captureChildArg})
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile("sentinels")
	require.NoError(t, err)
	var value string
	require.NoError(t, json.Unmarshal(data, &value))
	require.Equal(t, sentinelCanary, value)
}
