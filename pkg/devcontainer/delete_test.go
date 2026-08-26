package devcontainer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	testContainerID   = "container-abc"
	testStatusRunning = "running"
)

type mockDriver struct {
	findResult   *config.ContainerDetails
	findErr      error
	stopCalled   bool
	stopErr      error
	deleteCalled bool
	deleteErr    error
}

func (m *mockDriver) FindDevContainer(
	_ context.Context,
	_ string,
) (*config.ContainerDetails, error) {
	return m.findResult, m.findErr
}

func (m *mockDriver) StopDevContainer(_ context.Context, _ string) error {
	m.stopCalled = true
	return m.stopErr
}

func (m *mockDriver) DeleteDevContainer(_ context.Context, _ string) error {
	m.deleteCalled = true
	return m.deleteErr
}

//nolint:revive // interface implementation requires 7 args
func (m *mockDriver) CommandDevContainer(_ context.Context, _ *driver.CommandParams) error {
	return nil
}

func (m *mockDriver) RunDevContainer(_ context.Context, _ string, _ *driver.RunOptions) error {
	return nil
}

func (m *mockDriver) TargetArchitecture(_ context.Context, _ string) (string, error) {
	return "amd64", nil
}

func (m *mockDriver) StartDevContainer(_ context.Context, _ string) error {
	return nil
}

func (m *mockDriver) GetDevContainerLogs(
	_ context.Context, _ string, _ io.Writer, _ io.Writer,
) error {
	return nil
}

func newTestRunner(d driver.Driver) *runner {
	return &runner{
		driver: d,
		id:     "test-workspace",
		workspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.CustomDriver,
			},
		},
	}
}

func TestDelete_NilContainer_ReturnsNil(t *testing.T) {
	d := &mockDriver{findResult: nil}
	r := newTestRunner(d)

	err := r.Delete(context.Background(), DeleteOptions{})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if d.stopCalled {
		t.Error("StopDevContainer should not be called when container is nil")
	}
	if d.deleteCalled {
		t.Error("DeleteDevContainer should not be called when container is nil")
	}
}

func TestDelete_FindError_ReturnsError(t *testing.T) {
	d := &mockDriver{findErr: fmt.Errorf("connection refused")}
	r := newTestRunner(d)

	err := r.Delete(context.Background(), DeleteOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !searchString(err.Error(), "find dev container") {
		t.Errorf("expected wrapped find error, got: %v", err)
	}
}

func TestDelete_RunningContainer_StopsDeletesAndCleansUp(t *testing.T) {
	d := &mockDriver{
		findResult: &config.ContainerDetails{
			ID:     testContainerID,
			State:  config.ContainerDetailsState{Status: testStatusRunning},
			Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
		},
	}
	r := newTestRunner(d)

	err := r.Delete(context.Background(), DeleteOptions{})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !d.stopCalled {
		t.Error("expected StopDevContainer to be called for running container")
	}
	if !d.deleteCalled {
		t.Error("expected DeleteDevContainer to be called")
	}
}

func TestDelete_StoppedContainer_SkipsStopAndDeletes(t *testing.T) {
	d := &mockDriver{
		findResult: &config.ContainerDetails{
			ID:     testContainerID,
			State:  config.ContainerDetailsState{Status: "exited"},
			Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
		},
	}
	r := newTestRunner(d)

	err := r.Delete(context.Background(), DeleteOptions{})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if d.stopCalled {
		t.Error("StopDevContainer should not be called for stopped container")
	}
	if !d.deleteCalled {
		t.Error("expected DeleteDevContainer to be called")
	}
}

func TestDelete_DeleteError_ReturnsError(t *testing.T) {
	d := &mockDriver{
		findResult: &config.ContainerDetails{
			ID:     testContainerID,
			State:  config.ContainerDetailsState{Status: "exited"},
			Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
		},
		deleteErr: fmt.Errorf("permission denied"),
	}
	r := newTestRunner(d)

	err := r.Delete(context.Background(), DeleteOptions{})

	if err == nil {
		t.Fatal("expected error from DeleteDevContainer, got nil")
	}
}

func TestDelete_ImportedWorkspace_SkipsContainerCleansLeftovers(t *testing.T) {
	d := &mockDriver{
		findResult: &config.ContainerDetails{
			ID:     testContainerID,
			State:  config.ContainerDetailsState{Status: testStatusRunning},
			Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
		},
	}
	r := newTestRunner(d)
	r.workspaceConfig.Workspace = &provider.Workspace{
		Source: provider.WorkspaceSource{Container: "foreign-container"},
	}

	if err := r.Delete(context.Background(), DeleteOptions{}); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if d.stopCalled {
		t.Error("foreign container must never be stopped")
	}
	if d.deleteCalled {
		t.Error("foreign container must never be deleted")
	}
}

func TestDelete_RequiredFailure_StillRunsLeftoverSteps(t *testing.T) {
	ws := t.TempDir()
	external := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, external, `{"image":"alpine"}`)

	d := &mockDriver{
		findResult: &config.ContainerDetails{
			ID:     testContainerID,
			State:  config.ContainerDetailsState{Status: "exited"},
			Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
		},
		deleteErr: fmt.Errorf("daemon unreachable"),
	}
	r := newTestRunner(d)
	r.localWorkspaceFolder = ws
	r.workspaceConfig.Workspace = &provider.Workspace{
		Source: provider.WorkspaceSource{LocalFolder: ws},
	}

	if _, err := r.importExternalDevContainer(external); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	err := r.Delete(context.Background(), DeleteOptions{})
	if err == nil {
		t.Fatal("expected the container-delete failure to propagate")
	}
	if dirExists(importedProfilePath(ws)) {
		t.Error("leftover cleanup must still run after a required step fails")
	}
}

func TestDelete_RemovesImportedDevContainer(t *testing.T) {
	ws := t.TempDir()
	external := filepath.Join(t.TempDir(), "devcontainer.json")
	writeFile(t, external, `{"image":"alpine"}`)

	r := newTestRunner(&mockDriver{findResult: nil})
	r.localWorkspaceFolder = ws
	r.workspaceConfig.Workspace = &provider.Workspace{
		Source: provider.WorkspaceSource{LocalFolder: ws},
	}

	if _, err := r.importExternalDevContainer(external); err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if !dirExists(importedProfilePath(ws)) {
		t.Fatal("expected imported profile before delete")
	}

	if err := r.Delete(context.Background(), DeleteOptions{}); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dirExists(importedProfilePath(ws)) {
		t.Error("imported profile should be removed after Delete")
	}
}

func TestDelete_NonLocalSource_KeepsNothingToClean(t *testing.T) {
	r := newTestRunner(&mockDriver{findResult: nil})
	if err := r.Delete(context.Background(), DeleteOptions{}); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestTeardown_DeliveryVolumeFailure_WarnsButSucceeds(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)

	r := newTestRunner(&mockDriver{})
	r.workspaceConfig.Agent.Driver = provider.DockerDriver
	r.workspaceConfig.Agent.Docker = provider.ProviderDockerDriverConfig{
		Path: "devsy-test-nonexistent-docker-binary",
	}

	err := r.buildTeardownPlan(nil, DeleteOptions{}).execute(context.Background())
	if err != nil {
		t.Fatalf("best-effort failure must not fail teardown, got: %v", err)
	}
	if !observedWarning(logs.All(), "delivery volume cleanup") {
		t.Fatal("expected a warning mentioning delivery volume cleanup")
	}
}

func observedWarning(entries []observer.LoggedEntry, substr string) bool {
	for _, e := range entries {
		if e.Level == zapcore.WarnLevel && searchString(e.Message, substr) {
			return true
		}
	}
	return false
}

func TestTeardown_ImportedMarkerFailure_WarnsButSucceeds(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)

	tmpDir := t.TempDir()
	notADir := filepath.Join(tmpDir, importedProfileParent)
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	r := newTestRunner(&mockDriver{})
	r.localWorkspaceFolder = tmpDir
	r.workspaceConfig.Workspace = &provider.Workspace{
		Source: provider.WorkspaceSource{LocalFolder: tmpDir},
	}

	err := r.buildTeardownPlan(nil, DeleteOptions{}).execute(context.Background())
	if err != nil {
		t.Fatalf("best-effort failure must not fail teardown, got: %v", err)
	}
	if !observedWarning(logs.All(), "imported devcontainer cleanup") {
		t.Fatal("expected a warning mentioning imported devcontainer cleanup")
	}
}
