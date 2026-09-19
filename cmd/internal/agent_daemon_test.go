package cmdinternal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEcho = "echo"

type recordingDiagnostics struct {
	events   []machinediagnostics.Event
	statuses []machinediagnostics.Status
}

func (r *recordingDiagnostics) Record(event machinediagnostics.Event) {
	r.events = append(r.events, event)
}
func (r *recordingDiagnostics) Update(status machinediagnostics.Status) {
	r.statuses = append(r.statuses, status)
}
func (*recordingDiagnostics) Close() error { return nil }

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

func workspaceWithShutdownAction(action string) *provider2.AgentWorkspaceInfo {
	ws := &provider2.AgentWorkspaceInfo{Workspace: &provider2.Workspace{ID: "ws-test"}}
	if action != "" {
		ws.LastDevContainerConfig = &config.DevContainerConfigWithPath{
			Config: &config.DevContainerConfig{
				DevContainerConfigBase: config.DevContainerConfigBase{ShutdownAction: action},
			},
		}
	}
	return ws
}

func TestEffectiveShutdownAction(t *testing.T) {
	t.Run("prefers per-workspace config over flag", func(t *testing.T) {
		cmd := &DaemonCmd{ShutdownAction: config.ShutdownActionStopContainer}
		ws := workspaceWithShutdownAction(config.ShutdownActionNone)
		assert.Equal(t, config.ShutdownActionNone, cmd.effectiveShutdownAction(ws))
	})

	t.Run("falls back to flag when workspace has no config", func(t *testing.T) {
		cmd := &DaemonCmd{ShutdownAction: config.ShutdownActionNone}
		ws := workspaceWithShutdownAction("")
		assert.Equal(t, config.ShutdownActionNone, cmd.effectiveShutdownAction(ws))
	})

	t.Run("falls back to flag when config action is empty", func(t *testing.T) {
		cmd := &DaemonCmd{ShutdownAction: config.ShutdownActionNone}
		ws := &provider2.AgentWorkspaceInfo{
			Workspace: &provider2.Workspace{ID: "ws-test"},
			LastDevContainerConfig: &config.DevContainerConfigWithPath{
				Config: &config.DevContainerConfig{},
			},
		}
		assert.Equal(t, config.ShutdownActionNone, cmd.effectiveShutdownAction(ws))
	})

	t.Run("falls back to flag for nil workspace", func(t *testing.T) {
		cmd := &DaemonCmd{ShutdownAction: config.ShutdownActionStopContainer}
		assert.Equal(t, config.ShutdownActionStopContainer, cmd.effectiveShutdownAction(nil))
	})
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

func TestDaemonStateLocation(t *testing.T) {
	t.Run("uses explicit canonical state", func(t *testing.T) {
		cmd := &DaemonCmd{StateRoot: "/state", StateLayout: "canonical"}
		location, err := cmd.stateLocation()
		require.NoError(t, err)
		assert.Equal(t, "/state", location.Root)
		assert.Equal(t, "canonical", string(location.Layout))
	})

	t.Run("supports legacy agent directory", func(t *testing.T) {
		cmd := &DaemonCmd{GlobalFlags: &flags.GlobalFlags{}}
		cmd.AgentDir = "/state/agent"
		location, err := cmd.stateLocation()
		require.NoError(t, err)
		assert.Equal(t, "/state/agent", location.Root)
		assert.Equal(t, "agent-home", string(location.Layout))
	})

	t.Run("rejects incomplete state location", func(t *testing.T) {
		_, err := (&DaemonCmd{StateRoot: "/state"}).stateLocation()
		require.ErrorContains(t, err, "provided together")
	})
}

func TestDaemonWorkspaceConfigsSupportsBothLayouts(t *testing.T) {
	root := t.TempDir()
	canonical := writeWorkspaceConfig(
		t,
		filepath.Join(root, "contexts", "default", "workspaces", "canonical", "agent"),
		types.StrArray{testEcho},
	)
	agentHome := writeWorkspaceConfig(
		t,
		filepath.Join(root, "contexts", "default", "workspaces", "agent-home"),
		types.StrArray{testEcho},
	)

	t.Run("canonical", func(t *testing.T) {
		base, configs, err := (&DaemonCmd{StateRoot: root, StateLayout: "canonical"}).workspaceConfigs()
		require.NoError(t, err)
		assert.Equal(t, root, base)
		assert.Equal(t, []string{canonical}, configs)
	})

	t.Run("agent home", func(t *testing.T) {
		base, configs, err := (&DaemonCmd{StateRoot: root, StateLayout: "agent-home"}).workspaceConfigs()
		require.NoError(t, err)
		assert.Equal(t, root, base)
		assert.Equal(t, []string{agentHome}, configs)
	})
}

func TestDiagnosticsWorkspaceStatuses(t *testing.T) {
	root := t.TempDir()
	active := writeWorkspaceConfig(t, filepath.Join(root, "contexts", "default", "workspaces", "active", "agent"), types.StrArray{testEcho})
	notConfigured := writeWorkspaceConfig(t, filepath.Join(root, "contexts", "default", "workspaces", "not-configured", "agent"), nil)
	invalid := filepath.Join(root, "contexts", "default", "workspaces", "invalid", "agent", provider2.WorkspaceConfigFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(invalid), 0o750))
	require.NoError(t, os.WriteFile(invalid, []byte("invalid"), 0o600))

	statuses := diagnosticsWorkspaceStatuses([]string{active, notConfigured, invalid})
	require.Len(t, statuses, 3)
	assert.Equal(t, machinediagnostics.WorkspaceActive, statuses[0].State)
	assert.Equal(t, machinediagnostics.WorkspaceNotConfigured, statuses[1].State)
	assert.Equal(t, "invalid", statuses[2].ID)
	assert.Equal(t, machinediagnostics.WorkspaceInvalidConfig, statuses[2].State)
}

func TestRecordWorkspaceTransitions(t *testing.T) {
	recorder := &recordingDiagnostics{}
	cmd := &DaemonCmd{recorder: recorder}
	cmd.recordWorkspaceTransitions([]machinediagnostics.WorkspaceStatus{{ID: "one"}, {ID: "two"}})
	cmd.recordWorkspaceTransitions([]machinediagnostics.WorkspaceStatus{{ID: "two"}, {ID: "three"}})
	require.Len(t, recorder.events, 4)
	assert.Equal(t, machinediagnostics.EventWorkspaceDiscovered, recorder.events[0].Type)
	assert.Equal(t, "one", recorder.events[0].WorkspaceID)
	assert.Equal(t, machinediagnostics.EventWorkspaceDiscovered, recorder.events[2].Type)
	assert.Equal(t, "three", recorder.events[2].WorkspaceID)
	assert.Equal(t, machinediagnostics.EventWorkspaceRemoved, recorder.events[3].Type)
	assert.Equal(t, "one", recorder.events[3].WorkspaceID)
}

func TestRecordWorkspaceTransitionsReportsInvalidConfigOnce(t *testing.T) {
	recorder := &recordingDiagnostics{}
	cmd := &DaemonCmd{recorder: recorder}
	invalid := []machinediagnostics.WorkspaceStatus{{ID: "broken", State: machinediagnostics.WorkspaceInvalidConfig}}
	cmd.recordWorkspaceTransitions(invalid)
	cmd.recordWorkspaceTransitions(invalid)
	require.Len(t, recorder.events, 2)
	assert.Equal(t, machinediagnostics.EventWorkspaceDiscovered, recorder.events[0].Type)
	assert.Equal(t, machinediagnostics.EventWorkspaceConfigErr, recorder.events[1].Type)
}

func TestUpdateDiagnosticsPreservesLastSuccessfulPatrolAfterFailure(t *testing.T) {
	recorder := &recordingDiagnostics{}
	cmd := &DaemonCmd{recorder: recorder, startedAt: time.Now().UTC(), Interval: "1m"}
	cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonHealthy, nil, nil, nil)
	cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonDegraded, &machinediagnostics.DiagnosticError{Code: "patrol_failed"}, nil, nil)
	require.Len(t, recorder.statuses, 2)
	require.NotNil(t, recorder.statuses[0].LastSuccessAt)
	require.NotNil(t, recorder.statuses[1].LastSuccessAt)
	assert.Equal(t, *recorder.statuses[0].LastSuccessAt, *recorder.statuses[1].LastSuccessAt)
}

func TestDiagnosticsRetainsErrorHistoryWithoutInventingStartupPatrol(t *testing.T) {
	recorder := &recordingDiagnostics{}
	cmd := &DaemonCmd{recorder: recorder}
	cmd.updateDiagnostics(machinediagnostics.DaemonStarting, machinediagnostics.DaemonHealthy, nil, nil, nil)
	assert.Nil(t, recorder.statuses[0].LastPatrolAt)
	cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonDegraded, &machinediagnostics.DiagnosticError{Code: "shutdown_failed"}, nil, nil)
	cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonHealthy, nil, nil, nil)
	assert.Equal(t, "shutdown_failed", recorder.statuses[2].LastError.Code)
	assert.Equal(t, machinediagnostics.DaemonHealthy, recorder.statuses[2].Health)
}

func TestInvalidTimeoutBlocksShutdown(t *testing.T) {
	for _, timeout := range []string{"invalid", "0s", "-1m"} {
		t.Run(timeout, func(t *testing.T) {
			path := writeWorkspaceConfig(t, t.TempDir(), types.StrArray{testEcho})
			info, err := agent.ParseAgentWorkspaceInfo(path)
			require.NoError(t, err)
			info.Agent.Timeout = timeout
			data, err := json.Marshal(info)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, data, 0o600))
			evaluation := evaluateMachineInactivity([]string{path}, time.Time{}, config.ShutdownActionStopContainer, time.Now().Add(24*time.Hour))
			assert.Nil(t, evaluation.candidate)
			require.Len(t, evaluation.statuses, 1)
			assert.Equal(t, machinediagnostics.WorkspaceInvalidConfig, evaluation.statuses[0].State)
			assert.True(t, evaluation.statuses[0].BlocksMachineShutdown)
		})
	}
}

func TestEvaluateMachineInactivityRequiresAllWorkspacesToBeIdle(t *testing.T) {
	root := t.TempDir()
	first := writeWorkspaceConfig(t, filepath.Join(root, "one"), types.StrArray{testEcho})
	second := writeWorkspaceConfig(t, filepath.Join(root, "two"), types.StrArray{testEcho})
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-2 * agent.DefaultInactivityTimeout)
	require.NoError(t, os.Chtimes(first, old, old))
	require.NoError(t, os.Chtimes(second, old, old))

	evaluation := evaluateMachineInactivity([]string{first, second}, time.Time{}, config.ShutdownActionStopContainer, now)
	require.NotNil(t, evaluation.workspace)
	require.NotNil(t, evaluation.candidate)
	assert.Equal(t, "all workspaces are idle", evaluation.reason)

	require.NoError(t, os.Chtimes(second, now, now))
	evaluation = evaluateMachineInactivity([]string{first, second}, time.Time{}, config.ShutdownActionStopContainer, now)
	assert.Nil(t, evaluation.workspace)
	assert.Equal(t, "Waiting for inactivity deadline", evaluation.reason)
	assert.Len(t, evaluation.statuses, 2)
}

func TestEvaluateMachineInactivityUsesHeartbeatAndBusyGrace(t *testing.T) {
	configPath := writeWorkspaceConfig(t, t.TempDir(), types.StrArray{testEcho})
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-2 * agent.DefaultInactivityTimeout)
	require.NoError(t, os.Chtimes(configPath, old, old))
	heartbeat := now.Add(-time.Minute)
	evaluation := evaluateMachineInactivity([]string{configPath}, heartbeat, config.ShutdownActionStopContainer, now)
	require.Len(t, evaluation.statuses, 1)
	assert.Equal(t, machinediagnostics.WorkspaceActive, evaluation.statuses[0].State)
	assert.Equal(t, heartbeat, *evaluation.statuses[0].LastActivityAt)

	agent.CreateWorkspaceBusyFile(filepath.Dir(configPath))
	evaluation = evaluateMachineInactivity([]string{configPath}, time.Time{}, config.ShutdownActionStopContainer, now)
	assert.Equal(t, machinediagnostics.WorkspaceBusy, evaluation.statuses[0].State)
	require.NotNil(t, evaluation.statuses[0].IdleDeadlineAt)
	assert.WithinDuration(t, old.Add(busyGracePeriod).Add(agent.DefaultInactivityTimeout), *evaluation.statuses[0].IdleDeadlineAt, time.Second)
}

func TestEvaluateMachineInactivityBlocksDisabledWorkspace(t *testing.T) {
	configPath := writeWorkspaceConfig(t, t.TempDir(), types.StrArray{testEcho})
	evaluation := evaluateMachineInactivity([]string{configPath}, time.Time{}, config.ShutdownActionNone, time.Now())
	assert.Nil(t, evaluation.workspace)
	require.Len(t, evaluation.statuses, 1)
	assert.True(t, evaluation.statuses[0].BlocksMachineShutdown)
	assert.Equal(t, "Auto-stop is not configured", evaluation.statuses[0].BlockerReason)
}
