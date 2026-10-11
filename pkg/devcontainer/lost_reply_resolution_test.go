package devcontainer

import (
	"bufio"
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
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This matches the SDK delegate of cmd/internal/runtime_supervisor.go. Importing
// cmd here would cycle through devcontainer; Layer A separately covers Cobra.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 &&
		os.Args[1] == resolutionInternalCommand &&
		os.Args[2] == "runtime-supervisor" {
		supervisor.Main(os.Args[3:])
		return
	}
	os.Exit(m.Run())
}

const (
	resolutionInternalCommand = "internal"
	resolutionDisabledSetting = "none"
	resolutionReplyNonceLabel = "fixture.lost-reply-nonce"
	resolutionWorkspaceMount  = "/workspaces/lost-reply"
)

type resolutionReplyFixture struct {
	info                           *provider.AgentWorkspaceInfo
	state, record, sentinel, nonce string
	recordBytes                    []byte
}

type resolutionPersistedReply struct {
	Workspace, Nonce, Mode string
	Container              *runtimev1.ContainerDetails
}

type resolutionReplyOperation struct {
	Operation, Workspace, Nonce string
}

type resolutionReplyProcess struct {
	PID     int32
	Started int64
	Kind    string
}

func TestResolveContainerReusesPersistedCreateAfterLostReply(t *testing.T) {
	f := newResolutionReplyFixture(t)
	marker := f.createWithLostReply(t)
	f.requireProcessesGone(t)
	persisted, err := agent.ParseAgentWorkspaceInfo(f.record)
	require.NoError(t, err)
	require.Equal(t, f.info.Workspace.UID, persisted.Workspace.UID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publicRunner, err := NewRunner(ctx, "", "", persisted)
	require.NoError(t, err)
	fresh, ok := publicRunner.(*runner)
	require.True(t, ok)
	found, err := fresh.findExistingDevContainer(ctx)
	require.NoError(t, err)
	require.NotNil(t, found)
	f.assertAcknowledgedContainer(t, marker, found)
	params := unchangedLostReplyResolveParams()
	resolved, err := fresh.resolveContainer(ctx, params, found)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.NotNil(t, resolved.details)
	require.NotNil(t, resolved.mergedConfig)
	require.False(t, params.options.Recreate)
	assert.Equal(t, resolutionWorkspaceMount, params.substitutionContext.ContainerWorkspaceFolder,
		"existing-container branch must adopt its actual persisted bind destination")
	assert.Equal(t, containerRootUser, resolved.mergedConfig.RemoteUser)
	assert.Equal(t, found, resolved.details)
	again, err := fresh.findExistingDevContainer(ctx)
	require.NoError(t, err)
	assert.Equal(t, found, again)
	f.assertOperationCounts(t)
	data, err := os.ReadFile(f.record)
	require.NoError(t, err)
	assert.Equal(t, f.recordBytes, data)
	data, err = os.ReadFile(f.sentinel)
	require.NoError(t, err)
	assert.Equal(t, f.nonce, string(data))
	f.requireProcessesGone(t)
}

func unchangedLostReplyResolveParams() *resolveParams {
	current := &config.DevContainerConfig{}
	current.Image = "fixture"
	current.RemoteUser = containerRootUser
	current.ContainerUser = containerRootUser
	return &resolveParams{
		parsedConfig: &config.SubstitutedConfig{Config: current, Raw: current},
		substitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/computed-before-discovery",
		},
	}
}

func newResolutionReplyFixture(t *testing.T) *resolutionReplyFixture {
	t.Helper()
	f := &resolutionReplyFixture{state: t.TempDir(), nonce: encoding.CreateNewUID("", "reply")}
	origin, source := t.TempDir(), t.TempDir()
	name, checksum := buildResolutionRuntime(t, origin)
	f.info = &provider.AgentWorkspaceInfo{
		Origin: origin, ContentFolder: source,
		Workspace: &provider.Workspace{
			ID: "lost-resolution", UID: encoding.CreateNewUID("default", "resolve"),
			Source: provider.WorkspaceSource{LocalFolder: source},
		},
		Agent: provider.ProviderAgentConfig{
			Driver: provider.ExternalDriver,
			Docker: provider.ProviderDockerDriverConfig{
				Runtime:   "docker",
				Elevation: resolutionDisabledSetting,
			},
			External: provider.ProviderExternalDriverConfig{
				Binary:       "RUNTIME",
				ImageBackend: resolutionDisabledSetting,
				Args: []string{
					"--state-dir",
					f.state,
					"--mode",
					"persist-run-then-exit",
					"--reply-nonce",
					f.nonce,
				},
			},
			Binaries: map[string][]*provider.ProviderBinary{"RUNTIME": {{
				OS: runtime.GOOS, Arch: runtime.GOARCH, Path: name, Checksum: checksum,
			}}},
		},
	}
	require.Equal(t, f.info.Workspace.UID, GetRunnerIDFromWorkspace(f.info.Workspace))
	f.record = filepath.Join(origin, provider.WorkspaceConfigFile)
	recordBytes, err := json.Marshal(f.info)
	f.recordBytes = recordBytes
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.record, f.recordBytes, 0o600))
	f.sentinel = filepath.Join(source, "owned-user-data")
	require.NoError(t, os.WriteFile(f.sentinel, []byte(f.nonce), 0o600))
	t.Cleanup(func() { f.cleanup(t) })
	return f
}

func buildResolutionRuntime(t *testing.T, origin string) (string, string) {
	t.Helper()
	directory := filepath.Join(origin, "binaries", "runtime")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	name := "resolution runtime"
	if runtime.GOOS == goosWindows {
		name += ".exe"
	}
	binary := filepath.Join(directory, name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// #nosec G204 -- Builds the existing trusted fixture command into this test's private origin.
	build := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-o",
		binary,
		"../driver/external/internal/testfixture",
	)
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	// #nosec G304 -- Binary is built from the trusted fixture into this test-owned origin.
	bytes, err := os.ReadFile(binary)
	require.NoError(t, err)
	checksum := sha256.Sum256(bytes)
	return name, hex.EncodeToString(checksum[:])
}

func (f *resolutionReplyFixture) createWithLostReply(t *testing.T) resolutionPersistedReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := NewRunner(ctx, "", "", f.info)
	require.NoError(t, err)
	initial, ok := first.(*runner)
	require.True(t, ok)
	imageRunner, ok := initial.driver.(driver.ImageRunner)
	require.True(t, ok)
	err = imageRunner.RunImageDevContainer(ctx, &driver.RunImageDevContainerParams{
		WorkspaceID: f.info.Workspace.UID,
		Options: &driver.RunOptions{
			Image:      "fixture",
			User:       containerRootUser,
			RemoteUser: containerRootUser,
			Labels:     []string{resolutionReplyNonceLabel + "=" + f.nonce},
			WorkspaceMount: &config.Mount{
				Type:   driver.MountTypeBind,
				Source: f.info.ContentFolder,
				Target: resolutionWorkspaceMount,
			},
		},
	})
	require.Error(t, err)
	require.NoError(t, ctx.Err(), "intentional exit must fail before caller deadline")
	require.NotErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(filepath.Join(f.state, "reply-persisted.json"))
	require.NoError(t, err)
	var marker resolutionPersistedReply
	require.NoError(t, json.Unmarshal(data, &marker))
	require.Equal(t, f.nonce, marker.Nonce)
	require.Equal(t, f.info.Workspace.UID, marker.Workspace)
	require.Equal(t, "persist-run-then-exit", marker.Mode)
	require.NotNil(t, marker.Container)
	require.NotNil(t, marker.Container.State)
	require.NotNil(t, marker.Container.Config)
	require.Equal(t, f.info.Workspace.UID, marker.Container.Id)
	require.NotEmpty(t, marker.Container.CreatedAt)
	require.Equal(t, "running", marker.Container.State.Status)
	return marker
}

func (f *resolutionReplyFixture) assertAcknowledgedContainer(
	t *testing.T,
	marker resolutionPersistedReply,
	found *config.ContainerDetails,
) {
	t.Helper()
	assert.Equal(t, marker.Container.Id, found.ID)
	assert.Equal(t, marker.Container.CreatedAt, found.Created)
	assert.Equal(t, config.ContainerStatusRunning, found.State.Status)
	assert.Equal(t, marker.Container.State.StartedAt, found.State.StartedAt)
	assert.Equal(t, marker.Container.Config.Labels, found.Config.Labels)
	assert.Equal(t, f.nonce, found.Config.Labels[resolutionReplyNonceLabel])
	assert.Equal(t, containerRootUser, found.Config.User)
	require.Len(t, marker.Container.Mounts, 1)
	require.Len(t, found.Mounts, 1)
	assert.Equal(t, marker.Container.Mounts[0].Source, found.Mounts[0].Source)
	assert.Equal(t, marker.Container.Mounts[0].Destination, found.Mounts[0].Destination)
	assert.Equal(t, marker.Container.Mounts[0].Type, found.Mounts[0].Type)
}

func (f *resolutionReplyFixture) assertOperationCounts(t *testing.T) {
	t.Helper()
	records, err := resolutionReplyRecords[resolutionReplyOperation](
		filepath.Join(f.state, "reply-operations.jsonl"),
	)
	require.NoError(t, err)
	counts := map[string]int{}
	for _, record := range records {
		require.Equal(t, f.nonce, record.Nonce)
		require.Equal(t, f.info.Workspace.UID, record.Workspace)
		counts[record.Operation]++
	}
	require.Equal(t, map[string]int{"attempt": 1, "created": 1}, counts,
		"ordinary resolver must neither retry creation nor stop/delete existing state")
}

func resolutionReplyRecords[T any](path string) ([]T, error) {
	// #nosec G304 -- Callers pass only files written by the owned subprocess fixture.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var records []T
	scan := bufio.NewScanner(file)
	for scan.Scan() {
		var record T
		if err := json.Unmarshal(scan.Bytes(), &record); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		records = append(records, record)
	}
	return records, errors.Join(scan.Err(), file.Close())
}

func resolutionReplyProcessAlive(identity resolutionReplyProcess) (*process.Process, bool, error) {
	if identity.PID <= 0 || identity.Started <= 0 {
		return nil, false, fmt.Errorf("invalid owned process identity: %+v", identity)
	}
	p, err := process.NewProcess(identity.PID)
	if errors.Is(err, process.ErrorProcessNotRunning) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	started, err := p.CreateTime()
	if err != nil {
		exists, existErr := process.PidExists(identity.PID)
		if existErr == nil && !exists {
			return nil, false, nil
		}
		return nil, false, errors.Join(err, existErr)
	}
	return p, started == identity.Started, nil
}

func (f *resolutionReplyFixture) requireProcessesGone(t *testing.T) {
	t.Helper()
	records, err := resolutionReplyRecords[resolutionReplyProcess](
		filepath.Join(f.state, "reply-processes.jsonl"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, records)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, identity := range records {
			_, alive, err := resolutionReplyProcessAlive(identity)
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
	t.Logf("all %d recorded plugin/supervisor identities exited", len(records))
}

func (f *resolutionReplyFixture) terminateOwnedProcesses(t *testing.T) {
	t.Helper()
	records, err := resolutionReplyRecords[resolutionReplyProcess](
		filepath.Join(f.state, "reply-processes.jsonl"),
	)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if !assert.NoError(t, err) {
		return
	}
	for _, identity := range records {
		p, alive, err := resolutionReplyProcessAlive(identity)
		if !assert.NoError(t, err) || !alive {
			continue
		}
		// PID and birth time are checked immediately before failure-only cleanup.
		assert.NoError(t, p.Kill())
	}
}

func (f *resolutionReplyFixture) cleanup(t *testing.T) {
	f.terminateOwnedProcesses(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fresh, err := NewRunner(ctx, "", "", f.info)
	if assert.NoError(t, err) {
		assert.NoError(t, fresh.Delete(ctx, DeleteOptions{}))
	}
	f.terminateOwnedProcesses(t)
	f.requireProcessesGone(t)
}
