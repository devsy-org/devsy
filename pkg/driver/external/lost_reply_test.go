package external_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	containerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lostReplyRuntimeKey = "RUNTIME"
	lostReplyLabel      = "fixture.lost-reply-nonce"
	lostReplyUser       = "root"
	lostReplyContext    = "default"
	lostReplyExitMode   = "persist-run-then-exit"
	lostReplyHoldMode   = "persist-run-then-hold"
)

type lostReplyFixture struct {
	state, agentRecord, workspaceRecord, source, nonce string
	workspace                                          *provider.AgentWorkspaceInfo
	snapshots                                          map[string][]byte
}

type lostReplyMarker struct {
	Workspace string                      `json:"workspace"`
	Nonce     string                      `json:"nonce"`
	Mode      string                      `json:"mode"`
	Container *runtimev1.ContainerDetails `json:"container"`
}

type lostReplyOperation struct {
	Operation string `json:"operation"`
	Workspace string `json:"workspace"`
	Nonce     string `json:"nonce"`
}

type lostReplyProcess struct {
	PID     int32  `json:"pid"`
	Started int64  `json:"started"`
	Kind    string `json:"kind"`
}

// TestMain in production_test.go supplies the real CLI supervisor dispatch.
// These tests deliberately stop at public NewRunner/Find/Command, not Runner.Up.
func TestLostRunImageReplyFreshRunner(t *testing.T) {
	binary := buildLostReplyFixture(t)
	for _, mode := range []string{lostReplyExitMode, lostReplyHoldMode} {
		t.Run(mode, func(t *testing.T) {
			f := newLostReplyFixture(t, binary, mode)
			setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer setupCancel()
			bundle, err := drivercreate.New(setupCtx, f.workspace)
			require.NoError(t, err)
			imageRunner, ok := bundle.Runtime.(driver.ImageRunner)
			require.True(t, ok)
			marker := exerciseLostReply(t, f, imageRunner, mode)
			requireLostReplyProcessesGone(t, f.state)
			// Only persisted metadata crosses this boundary; no original Host is reused.
			f.assertFreshRunner(t, marker)
			f.assertPreservedFiles(t)
			f.assertOperations(t)
			requireLostReplyProcessesGone(t, f.state)
		})
	}
}

func buildLostReplyFixture(t *testing.T) string {
	t.Helper()
	name := "runtime space é"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// #nosec G204 -- Builds only the checked-in trusted fixture into a test-owned directory.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./internal/testfixture")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	return binary
}

func newLostReplyFixture(t *testing.T, binary, mode string) *lostReplyFixture {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	f := &lostReplyFixture{state: t.TempDir(), source: t.TempDir(), snapshots: map[string][]byte{}}
	f.nonce = encoding.CreateNewUID("", "reply")
	origin := t.TempDir()
	name, checksum := prepareLostReplyBinary(t, origin, binary)
	ssh := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(ssh, []byte("# owned SSH sentinel "+f.nonce+"\n"), 0o600))
	workspace := &provider.Workspace{
		ID:            "lost-reply-workspace",
		UID:           encoding.CreateNewUID(lostReplyContext, "lost-reply"),
		Context:       lostReplyContext,
		Provider:      provider.WorkspaceProviderConfig{Name: "lost-reply-fixture"},
		Source:        provider.WorkspaceSource{LocalFolder: f.source},
		SSHConfigPath: ssh,
	}
	require.NoError(t, provider.SaveWorkspaceConfig(workspace))
	hostDirectory, err := provider.GetWorkspaceDir(workspace.Context, workspace.ID)
	require.NoError(t, err)
	f.workspaceRecord = filepath.Join(hostDirectory, provider.WorkspaceConfigFile)
	f.workspace = &provider.AgentWorkspaceInfo{
		Origin: origin, Workspace: workspace, ContentFolder: f.source,
		Agent: provider.ProviderAgentConfig{
			Driver: provider.ExternalDriver,
			Docker: provider.ProviderDockerDriverConfig{Runtime: "docker", Elevation: "none"},
			External: provider.ProviderExternalDriverConfig{
				Binary: lostReplyRuntimeKey, ImageBackend: noImageBackend,
				Args: []string{"--state-dir", f.state, "--mode", mode, "--reply-nonce", f.nonce},
			},
			Binaries: map[string][]*provider.ProviderBinary{lostReplyRuntimeKey: {{
				OS: runtime.GOOS, Arch: runtime.GOARCH, Path: name, Checksum: checksum,
			}}},
		},
	}
	f.agentRecord = filepath.Join(origin, provider.WorkspaceConfigFile)
	raw, err := json.Marshal(f.workspace)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.agentRecord, raw, 0o600))
	sentinel := filepath.Join(f.source, "user-data")
	require.NoError(t, os.WriteFile(sentinel, []byte(f.nonce), 0o600))
	for _, path := range []string{f.agentRecord, f.workspaceRecord, sentinel, ssh} {
		// #nosec G304 -- Snapshots include only this fixture's records, source and SSH sentinel.
		f.snapshots[path], err = os.ReadFile(path)
		require.NoError(t, err)
	}
	// Registered after owned directories and before any transport operation.
	t.Cleanup(func() { f.cleanup(t) })
	return f
}

func prepareLostReplyBinary(t *testing.T, origin, binary string) (string, string) {
	t.Helper()
	directory := filepath.Join(origin, "binaries", "runtime")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	// #nosec G304 -- The binary is built by this package's test fixture.
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	name := filepath.Base(binary)
	// #nosec G306 G703 -- Trusted fixture bytes are copied under this test-owned origin and executable basename.
	require.NoError(t, os.WriteFile(filepath.Join(directory, name), data, 0o700))
	checksum := sha256.Sum256(data)
	return name, hex.EncodeToString(checksum[:])
}

func exerciseLostReply(
	t *testing.T,
	f *lostReplyFixture,
	imageRunner driver.ImageRunner,
	mode string,
) lostReplyMarker {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	result := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			joinLostReplyCall(t, f.state, result)
		}
	})
	go func() {
		result <- imageRunner.RunImageDevContainer(ctx, &driver.RunImageDevContainerParams{
			WorkspaceID: devcontainer.GetRunnerIDFromWorkspace(f.workspace.Workspace),
			Options: &driver.RunOptions{
				Image: "fixture", User: lostReplyUser, RemoteUser: lostReplyUser, Labels: []string{lostReplyLabel + "=" + f.nonce},
				WorkspaceMount: &containerconfig.Mount{Type: driver.MountTypeBind, Source: f.source, Target: "/workspace"},
			},
		})
	}()
	marker := waitForLostReplyMarker(t, f, mode)
	if mode == lostReplyHoldMode {
		cancel()
	}
	select {
	case err := <-result:
		joined = true
		require.Error(t, err)
		if mode == lostReplyHoldMode {
			require.ErrorIs(t, err, context.Canceled)
		} else {
			require.NoError(t, ctx.Err(), "crash must fail before caller deadline")
			require.NotErrorIs(t, err, context.Canceled)
			require.NotContains(t, err.Error(), "already exists")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lost-reply call did not return within cleanup bound")
	}
	cancel()
	return marker
}

func joinLostReplyCall(t *testing.T, state string, result <-chan error) {
	t.Helper()
	select {
	case <-result:
		return
	case <-time.After(10 * time.Second):
		terminateLostReplyProcesses(t, state)
	}
	select {
	case <-result:
	case <-time.After(10 * time.Second):
		t.Error("RunImage caller did not join after owned cleanup")
	}
}

func waitForLostReplyMarker(t *testing.T, f *lostReplyFixture, mode string) lostReplyMarker {
	t.Helper()
	var marker lostReplyMarker
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		data, err := os.ReadFile(filepath.Join(f.state, "reply-persisted.json"))
		if !assert.NoError(c, err) {
			return
		}
		assert.NoError(c, json.Unmarshal(data, &marker))
	}, 10*time.Second, 10*time.Millisecond, "must observe a complete persisted effect before triggering cancellation")
	require.Equal(t, f.nonce, marker.Nonce)
	require.Equal(t, mode, marker.Mode)
	require.Equal(t, f.workspace.Workspace.UID, marker.Workspace)
	require.NotNil(t, marker.Container)
	require.NotNil(t, marker.Container.State)
	require.NotNil(t, marker.Container.Config)
	require.Equal(t, marker.Workspace, marker.Container.Id)
	require.NotEmpty(t, marker.Container.CreatedAt)
	require.Equal(t, "running", marker.Container.State.Status)
	require.Equal(t, f.nonce, marker.Container.Config.Labels[lostReplyLabel])
	return marker
}

func (f *lostReplyFixture) assertFreshRunner(t *testing.T, marker lostReplyMarker) {
	t.Helper()
	persisted, err := agent.ParseAgentWorkspaceInfo(f.agentRecord)
	require.NoError(t, err)
	require.Equal(t, f.workspace.Workspace.UID, persisted.Workspace.UID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fresh, err := devcontainer.NewRunner(ctx, "", "", persisted)
	require.NoError(t, err)
	found, err := fresh.Find(ctx)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, marker.Container.Id, found.ID)
	assert.Equal(t, marker.Container.CreatedAt, found.Created)
	assert.Equal(t, containerconfig.ContainerStatusRunning, found.State.Status)
	assert.Equal(t, marker.Container.State.StartedAt, found.State.StartedAt)
	assert.Equal(t, marker.Container.Config.Labels, found.Config.Labels)
	assert.Equal(t, marker.Container.Config.User, found.Config.User)
	require.Len(t, marker.Container.Mounts, 1)
	require.Len(t, found.Mounts, 1)
	assert.Equal(t, marker.Container.Mounts[0].Source, found.Mounts[0].Source)
	assert.Equal(t, marker.Container.Mounts[0].Destination, found.Mounts[0].Destination)
	assert.Equal(t, marker.Container.Mounts[0].Type, found.Mounts[0].Type)
	var stdout, stderr bytes.Buffer
	require.NoError(t, fresh.Command(ctx, devcontainer.CommandParams{
		Command: "cat", Stdin: bytes.NewBufferString(f.nonce), Stdout: &stdout, Stderr: &stderr,
	}))
	assert.Equal(t, f.nonce, stdout.String(), "fixture echo proves transport usability only")
	again, err := fresh.Find(ctx)
	require.NoError(t, err)
	assert.Equal(t, found, again, "a transport command must not replace the persisted container")
}

func (f *lostReplyFixture) assertPreservedFiles(t *testing.T) {
	t.Helper()
	for path, want := range f.snapshots {
		// #nosec G304 -- Snapshot paths belong to this test's isolated workspace and SSH configuration.
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, want, got, path)
	}
	loaded, err := provider.LoadWorkspaceConfig(lostReplyContext, f.workspace.Workspace.ID)
	require.NoError(t, err)
	assert.Equal(t, f.workspace.Workspace.UID, loaded.UID)
	assert.Equal(t, f.workspace.Workspace.Source, loaded.Source)
	assert.Equal(t, f.workspace.Workspace.Provider, loaded.Provider)
}

func (f *lostReplyFixture) assertOperations(t *testing.T) {
	t.Helper()
	records, err := readLostReplyRecords[lostReplyOperation](
		filepath.Join(f.state, "reply-operations.jsonl"),
	)
	require.NoError(t, err)
	counts := map[string]int{}
	for _, record := range records {
		require.Equal(t, f.nonce, record.Nonce)
		require.Equal(t, f.workspace.Workspace.UID, record.Workspace)
		counts[record.Operation]++
	}
	assert.Equal(
		t,
		map[string]int{"attempt": 1, "created": 1},
		counts,
		"before cleanup: exactly one create attempt and effect, no stop/delete or hidden duplicate attempt",
	)
}

func readLostReplyRecords[T any](path string) ([]T, error) {
	// #nosec G304 -- Callers supply operation logs under this test's owned fixture directory.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var records []T
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record T
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		records = append(records, record)
	}
	return records, errors.Join(scanner.Err(), file.Close())
}

func lostReplyProcessAlive(identity lostReplyProcess) (bool, error) {
	if identity.PID <= 0 || identity.Started <= 0 {
		return false, fmt.Errorf("invalid owned process identity: %+v", identity)
	}
	p, err := process.NewProcess(identity.PID)
	if errors.Is(err, process.ErrorProcessNotRunning) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	started, err := p.CreateTime()
	if err != nil {
		exists, existErr := process.PidExists(identity.PID)
		if existErr == nil && !exists {
			return false, nil
		}
		return false, errors.Join(err, existErr)
	}
	return started == identity.Started, nil
}

func requireLostReplyProcessesGone(t *testing.T, directory string) {
	t.Helper()
	records, err := readLostReplyRecords[lostReplyProcess](
		filepath.Join(directory, "reply-processes.jsonl"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, records)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, identity := range records {
			alive, err := lostReplyProcessAlive(identity)
			assert.NoError(c, err)
			assert.False(
				c,
				alive,
				"owned %s pid %d/start %d must exit",
				identity.Kind,
				identity.PID,
				identity.Started,
			)
		}
	}, 10*time.Second, 20*time.Millisecond)
}

func terminateLostReplyProcesses(t *testing.T, directory string) {
	t.Helper()
	records, err := readLostReplyRecords[lostReplyProcess](
		filepath.Join(directory, "reply-processes.jsonl"),
	)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if !assert.NoError(t, err) {
		return
	}
	for _, identity := range records {
		terminateLostReplyProcess(t, identity)
	}
}

func terminateLostReplyProcess(t *testing.T, identity lostReplyProcess) {
	t.Helper()
	alive, err := lostReplyProcessAlive(identity)
	if !assert.NoError(t, err) || !alive {
		return
	}
	p, err := process.NewProcess(identity.PID)
	if !assert.NoError(t, err) {
		return
	}
	// Recheck immediately before killing; never intentionally kill a reused PID.
	started, err := p.CreateTime()
	if !assert.NoError(t, err) || started != identity.Started {
		return
	}
	// IsRunning refreshes identity; CreateTime on the same Process is cached.
	same, err := p.IsRunning()
	if assert.NoError(t, err) && same {
		assert.NoError(t, p.Kill())
	}
}

func (f *lostReplyFixture) cleanup(t *testing.T) {
	// The first-call join cleanup runs before this callback (LIFO).
	terminateLostReplyProcesses(t, f.state)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	persisted, err := agent.ParseAgentWorkspaceInfo(f.agentRecord)
	if assert.NoError(t, err) {
		bundle, err := drivercreate.New(ctx, persisted)
		if assert.NoError(t, err) {
			assert.NoError(t, bundle.Runtime.DeleteDevContainer(ctx, f.workspace.Workspace.UID))
		}
	}
	terminateLostReplyProcesses(t, f.state)
	requireLostReplyProcessesGone(t, f.state)
}
