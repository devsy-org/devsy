package devcontainer

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
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
func (m *mockDriver) CommandDevContainer(
	_ context.Context, _, _, _ string, _ io.Reader, _ io.Writer, _ io.Writer,
) error {
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
		Driver: d,
		ID:     "test-workspace",
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
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

func TestCleanupDeliveryVolume_DoesNotPanic(t *testing.T) {
	d := &mockDriver{}
	r := newTestRunner(d)

	r.cleanupDeliveryVolume(context.Background())
}
