package cmdinternal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEcho = "echo"

func writeWorkspaceConfig(t *testing.T, dir string, shutdown types.StrArray) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))

	info := &provider2.AgentWorkspaceInfo{Workspace: &provider2.Workspace{ID: "ws-test"}}
	info.Agent.Exec.Shutdown = shutdown

	data, err := json.Marshal(info)
	require.NoError(t, err)

	path := filepath.Join(dir, provider2.WorkspaceConfigFile)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestGetActivity_ShutdownConfigured(t *testing.T) {
	cfg := writeWorkspaceConfig(t, t.TempDir(), types.StrArray{testEcho, "stop"})

	activity, ws, err := getActivity(cfg)
	require.NoError(t, err)
	require.NotNil(t, activity)
	require.NotNil(t, ws)
	assert.Equal(t, "ws-test", ws.Workspace.ID)

	stat, err := os.Stat(cfg)
	require.NoError(t, err)
	assert.Equal(t, stat.ModTime(), *activity)
}

func TestGetActivity_NoShutdownReturnsNil(t *testing.T) {
	cfg := writeWorkspaceConfig(t, t.TempDir(), nil)

	activity, ws, err := getActivity(cfg)
	require.NoError(t, err)
	assert.Nil(t, activity)
	assert.Nil(t, ws)
}

func TestGetActivity_BusyFileAddsGrace(t *testing.T) {
	dir := t.TempDir()
	cfg := writeWorkspaceConfig(t, dir, types.StrArray{testEcho, "stop"})
	agent.CreateWorkspaceBusyFile(dir)

	activity, _, err := getActivity(cfg)
	require.NoError(t, err)
	require.NotNil(t, activity)

	stat, err := os.Stat(cfg)
	require.NoError(t, err)
	assert.Equal(t, stat.ModTime().Add(busyGracePeriod), *activity)
}

func TestGetActivity_ReadError(t *testing.T) {
	_, _, err := getActivity(filepath.Join(t.TempDir(), "missing.json"))
	assert.Error(t, err)
}

func TestFindLatestActivity_PicksLatest(t *testing.T) {
	base := t.TempDir()
	older := writeWorkspaceConfig(t, filepath.Join(base, "a"), types.StrArray{testEcho})
	newer := writeWorkspaceConfig(t, filepath.Join(base, "b"), types.StrArray{testEcho})

	oldTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(older, oldTime, oldTime))
	recentTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	require.NoError(t, os.Chtimes(newer, recentTime, recentTime))

	activity, ws := findLatestActivity([]string{older, newer})
	require.NotNil(t, activity)
	require.NotNil(t, ws)
	assert.Equal(t, recentTime, *activity)
}

func TestEffectiveActivity(t *testing.T) {
	orig := activityFilePath
	t.Cleanup(func() { activityFilePath = orig })

	configActivity := time.Now().Add(-30 * time.Minute).Truncate(time.Second)

	touch := func(name string, mtime time.Time) string {
		path := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		require.NoError(t, os.Chtimes(path, mtime, mtime))
		return path
	}

	activityFilePath = filepath.Join(t.TempDir(), "absent.activity")
	assert.Equal(t, configActivity, effectiveActivity(configActivity))

	freshTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	activityFilePath = touch("fresh.activity", freshTime)
	assert.Equal(t, freshTime, effectiveActivity(configActivity))

	activityFilePath = touch("stale.activity", time.Now().Add(-2*time.Hour).Truncate(time.Second))
	assert.Equal(t, configActivity, effectiveActivity(configActivity))
}
