package apple

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

// Test image references shared across driver tests.
const (
	testImage    = "img"
	testImageRef = "alpine:latest"
)

// mockClient is a test double for appleClient. It records every mutating call
// into a single ordered log (calls) so tests can assert relative ordering, not
// just that calls happened.
type mockClient struct {
	found      *config.ContainerDetails
	foundErr   error
	inspectErr error
	pulled     bool
	waitErr    error
	startErr   error
	stopErr    error
	calls      []string   // ordered event log, e.g. "stop:c1", "remove:c1"
	ranArgs    [][]string // exec/run argument lists, in call order
	runWithDir string     // dir passed to the last RunWithDir call
	systemDown bool       // SystemRunning reports false
	ensureErr  error      // error returned by EnsureSystemRunning
}

func (m *mockClient) EnsureSystemRunning(context.Context) error  { return m.ensureErr }
func (m *mockClient) SystemRunning(context.Context) bool         { return !m.systemDown }
func (m *mockClient) EnsureBuilderRunning(context.Context) error { return nil }
func (m *mockClient) FindDevContainer(context.Context, []string) (*config.ContainerDetails, error) {
	return m.found, m.foundErr
}

func (m *mockClient) FindContainerByID(
	context.Context, []string,
) (*config.ContainerDetails, error) {
	return m.found, m.foundErr
}

func (m *mockClient) InspectImage(context.Context, string, bool) (*config.ImageDetails, error) {
	if m.inspectErr != nil {
		return nil, m.inspectErr
	}
	return &config.ImageDetails{ID: testImage}, nil
}
func (m *mockClient) GetImageTag(context.Context, string) (string, error) { return "", nil }
func (m *mockClient) Pull(context.Context, apple.PullOptions) error {
	m.pulled = true
	m.calls = append(m.calls, "pull")
	return nil
}
func (m *mockClient) Push(context.Context, string, io.Writer, io.Writer) error { return nil }
func (m *mockClient) Tag(context.Context, string, string) error                { return nil }
func (m *mockClient) Run(_ context.Context, args []string, _ apple.Streams) error {
	m.calls = append(m.calls, "exec")
	m.ranArgs = append(m.ranArgs, args)
	return nil
}

func (m *mockClient) RunWithDir(
	_ context.Context,
	dir string,
	args []string,
	_ apple.Streams,
) error {
	m.calls = append(m.calls, "run")
	m.runWithDir = dir
	m.ranArgs = append(m.ranArgs, args)
	return nil
}

func (m *mockClient) StartContainer(_ context.Context, id string) error {
	m.calls = append(m.calls, "start:"+id)
	return m.startErr
}
func (m *mockClient) WaitContainerRunning(context.Context, string) error { return m.waitErr }
func (m *mockClient) Stop(_ context.Context, id string) error {
	m.calls = append(m.calls, "stop:"+id)
	return m.stopErr
}

func (m *mockClient) Remove(_ context.Context, id string) error {
	m.calls = append(m.calls, "remove:"+id)
	return nil
}

func (m *mockClient) GetContainerLogs(context.Context, string, io.Writer, io.Writer) error {
	return nil
}

func running(id string) *config.ContainerDetails {
	return &config.ContainerDetails{
		ID:    id,
		State: config.ContainerDetailsState{Status: config.ContainerStatusRunning},
	}
}

func TestFindDevContainer_InjectsUserLabel(t *testing.T) {
	c := &config.ContainerDetails{ID: "x", Config: config.ContainerDetailsConfig{User: "1000"}}
	d := &appleDriver{Apple: &mockClient{found: c}}

	got, err := d.FindDevContainer(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FindDevContainer: %v", err)
	}
	if got.Config.Labels[config.UserLabel] != "1000" {
		t.Errorf("UserLabel = %q, want 1000", got.Config.Labels[config.UserLabel])
	}
}

func TestFindDevContainer_UsesContainerIDWhenPinned(t *testing.T) {
	m := &mockClient{found: running("pinned")}
	d := &appleDriver{Apple: m, containerID: "pinned"}
	got, err := d.FindDevContainer(context.Background(), "ws")
	if err != nil || got == nil || got.ID != "pinned" {
		t.Fatalf("expected pinned container, got %+v err=%v", got, err)
	}
}

func TestDeleteDevContainer_StopsThenRemovesInOrder(t *testing.T) {
	m := &mockClient{found: running("c1")}
	d := &appleDriver{Apple: m}
	if err := d.DeleteDevContainer(context.Background(), "ws"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Order matters: Apple's delete requires the container be stopped first.
	if want := []string{"stop:c1", "remove:c1"}; !slices.Equal(m.calls, want) {
		t.Errorf("call order = %v, want %v", m.calls, want)
	}
}

func TestDeleteDevContainer_StopFailureStillRemoves(t *testing.T) {
	m := &mockClient{found: running("c1"), stopErr: errors.New("stop boom")}
	d := &appleDriver{Apple: m}
	if err := d.DeleteDevContainer(context.Background(), "ws"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// A stop failure is logged but delete is still attempted.
	if want := []string{"stop:c1", "remove:c1"}; !slices.Equal(m.calls, want) {
		t.Errorf("call order = %v, want %v", m.calls, want)
	}
}

func TestDeleteDevContainer_StoppedContainerSkipsStop(t *testing.T) {
	stopped := &config.ContainerDetails{
		ID:    "c3",
		State: config.ContainerDetailsState{Status: "exited"},
	}
	m := &mockClient{found: stopped}
	d := &appleDriver{Apple: m}
	if err := d.DeleteDevContainer(context.Background(), "ws"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if want := []string{"remove:c3"}; !slices.Equal(m.calls, want) {
		t.Errorf("stopped container should skip stop; calls = %v, want %v", m.calls, want)
	}
}

func TestDeleteDevContainer_NoContainerIsNoop(t *testing.T) {
	m := &mockClient{found: nil}
	d := &appleDriver{Apple: m}
	if err := d.DeleteDevContainer(context.Background(), "ws"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(m.calls) != 0 {
		t.Errorf("no container: expected no calls, got %v", m.calls)
	}
}

func TestRunImageDevContainer_PullsRunsThenUID(t *testing.T) {
	m := &mockClient{inspectErr: errors.New("absent")} // force a pull
	d := &appleDriver{Apple: m}
	err := d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
		WorkspaceID:          "ws",
		LocalWorkspaceFolder: "/local/ws",
		ParsedConfig:         &config.DevContainerConfig{},
		Options:              &driver.RunOptions{Image: testImageRef},
	})
	if err != nil {
		t.Fatalf("RunImageDevContainer: %v", err)
	}
	// Image must be ensured (pulled) before the container is run.
	if want := []string{"pull", "run"}; !slices.Equal(m.calls, want) {
		t.Errorf("call order = %v, want %v (ensure image before run)", m.calls, want)
	}
	if m.runWithDir != "/local/ws" {
		t.Errorf("run cwd = %q, want /local/ws", m.runWithDir)
	}
}

func TestEnsureImage_PullsWhenMissing(t *testing.T) {
	m := &mockClient{inspectErr: errors.New("not found")}
	d := &appleDriver{Apple: m}
	if err := d.EnsureImage(
		context.Background(),
		&driver.RunOptions{Image: testImage},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !m.pulled {
		t.Error("expected pull when image is missing locally")
	}
}

func TestEnsureImage_SkipsPullWhenPresent(t *testing.T) {
	m := &mockClient{} // InspectImage succeeds
	d := &appleDriver{Apple: m}
	if err := d.EnsureImage(
		context.Background(),
		&driver.RunOptions{Image: testImage},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if m.pulled {
		t.Error("must not pull when image already present")
	}
}

func TestCommandDevContainer_StartsStoppedContainer(t *testing.T) {
	stopped := &config.ContainerDetails{
		ID:    "c2",
		State: config.ContainerDetailsState{Status: "exited"},
	}
	m := &mockClient{found: stopped}
	d := &appleDriver{Apple: m}

	err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
		WorkspaceID: "ws", User: "root", Command: "echo hi",
	})
	if err != nil {
		t.Fatalf("CommandDevContainer: %v", err)
	}
	// A stopped container must be started before the exec runs.
	if want := []string{"start:c2", "exec"}; !slices.Equal(m.calls, want) {
		t.Errorf("call order = %v, want %v", m.calls, want)
	}
	last := m.ranArgs[len(m.ranArgs)-1]
	if last[0] != appleExec {
		t.Errorf("expected exec, got %v", last)
	}
}

func TestCommandDevContainer_NotFound(t *testing.T) {
	d := &appleDriver{Apple: &mockClient{found: nil}}
	err := d.CommandDevContainer(context.Background(), &driver.CommandParams{WorkspaceID: "ws"})
	if err == nil {
		t.Error("expected error when container not found")
	}
}
