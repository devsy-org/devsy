package cmdinternal

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
)

type blockingFindRunner struct{}

func (blockingFindRunner) Up(
	context.Context, devcontainer.UpOptions, time.Duration, status.Reporter,
) (*config.Result, error) {
	panic("not implemented")
}

func (blockingFindRunner) Build(context.Context, provider2.BuildOptions) (string, error) {
	panic("not implemented")
}

func (blockingFindRunner) Find(ctx context.Context) (*config.ContainerDetails, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingFindRunner) Command(context.Context, devcontainer.CommandParams) error {
	panic("not implemented")
}

func (blockingFindRunner) Stop(context.Context) error {
	panic("not implemented")
}

func (blockingFindRunner) Delete(context.Context, devcontainer.DeleteOptions) error {
	panic("not implemented")
}

func (blockingFindRunner) Logs(context.Context, io.Writer) error {
	panic("not implemented")
}

func TestStartDevContainer_FindIsBoundedByTimeout(t *testing.T) {
	original := findDevContainerTimeout
	findDevContainerTimeout = 50 * time.Millisecond
	defer func() { findDevContainerTimeout = original }()

	ctx := context.Background()

	start := time.Now()
	err := startDevContainer(ctx, &provider2.AgentWorkspaceInfo{}, blockingFindRunner{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("startDevContainer must return an error when Find never returns on its own")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped context.DeadlineExceeded, got %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("startDevContainer took %s, want bounded by findDevContainerTimeout", elapsed)
	}
}

type stubRunner struct {
	findResult *config.ContainerDetails
	findErr    error
	upErr      error
	commandErr error
}

func (s stubRunner) Up(
	context.Context, devcontainer.UpOptions, time.Duration, status.Reporter,
) (*config.Result, error) {
	return nil, s.upErr
}

func (stubRunner) Build(context.Context, provider2.BuildOptions) (string, error) {
	panic("not implemented")
}

func (s stubRunner) Find(context.Context) (*config.ContainerDetails, error) {
	return s.findResult, s.findErr
}

func (s stubRunner) Command(context.Context, devcontainer.CommandParams) error {
	return s.commandErr
}

func (stubRunner) Stop(context.Context) error {
	panic("not implemented")
}

func (stubRunner) Delete(context.Context, devcontainer.DeleteOptions) error {
	panic("not implemented")
}

func (stubRunner) Logs(context.Context, io.Writer) error {
	panic("not implemented")
}

func TestStartDevContainer_MissingContainerStartsItAndWrapsUpError(t *testing.T) {
	upErr := errors.New("boom")
	runner := stubRunner{findResult: nil, upErr: upErr}

	err := startDevContainer(context.Background(), &provider2.AgentWorkspaceInfo{}, runner)

	if !errors.Is(err, upErr) {
		t.Fatalf("expected wrapped %v, got %v", upErr, err)
	}
	const wantPrefix = "start container:"
	if got := err.Error(); len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("error %q does not start with %q", got, wantPrefix)
	}
}

func TestStartDevContainer_RunningContainerWithoutResultRestarts(t *testing.T) {
	upErr := errors.New("boom")
	runner := stubRunner{
		findResult: &config.ContainerDetails{
			State: config.ContainerDetailsState{Status: containerStatusRunning},
		},
		commandErr: errors.New("cat: no such file"), // hasDevContainerResult -> false
		upErr:      upErr,
	}
	workspaceConfig := &provider2.AgentWorkspaceInfo{
		Workspace: &provider2.Workspace{UID: "a"}, // legacy (non-UUID) UID
	}

	err := startDevContainer(context.Background(), workspaceConfig, runner)

	if !errors.Is(err, upErr) {
		t.Fatalf("expected wrapped %v, got %v", upErr, err)
	}
	const wantPrefix = "restart container after missing devcontainer result:"
	if got := err.Error(); len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("error %q does not start with %q", got, wantPrefix)
	}
}

func TestStartDevContainer_RunningContainerWithResultIsNoOp(t *testing.T) {
	runner := stubRunner{
		findResult: &config.ContainerDetails{
			State: config.ContainerDetailsState{Status: containerStatusRunning},
		},
		commandErr: nil, // hasDevContainerResult -> true
		upErr:      errors.New("Up must not be called on the happy path"),
	}
	workspaceConfig := &provider2.AgentWorkspaceInfo{
		Workspace: &provider2.Workspace{UID: "a"},
	}

	if err := startDevContainer(context.Background(), workspaceConfig, runner); err != nil {
		t.Fatalf("startDevContainer returned unexpected error: %v", err)
	}
}
