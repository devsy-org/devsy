package agentworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/assert"
)

type workspaceTestLogger struct{}

func (workspaceTestLogger) Debugf(string, ...any) {}
func (workspaceTestLogger) Info(...any)           {}
func (workspaceTestLogger) Infof(string, ...any)  {}
func (workspaceTestLogger) Warnf(string, ...any)  {}

var _ tunnelserver.Logger = workspaceTestLogger{}

func TestSplitSecrets(t *testing.T) {
	env, mount := splitSecrets([]*tunnel.Secret{
		{Name: "SESSION_SENTINEL", Value: "sentinel-value"},
		{Name: "MOUNT_SENTINEL", Value: "mount-value", Mount: true},
	})
	assert.Equal(t, []string{"SESSION_SENTINEL=sentinel-value"}, env)
	assert.Equal(t, []string{"MOUNT_SENTINEL=mount-value"}, mount)
}

func TestPrepareWorkspaceWithStatusCallsPreparationDirectly(t *testing.T) {
	assertWorkspacePreparationRunsDirectly(t, status.NewMemoryReporter(), "")
	assertWorkspacePreparationRunsDirectly(t, nil, status.PhaseResettingWorkspace)
}

func assertWorkspacePreparationRunsDirectly(
	t *testing.T,
	reporter status.Reporter,
	phase status.Phase,
) {
	t.Helper()
	ctx := context.Background()
	called := false
	prepare := func(got context.Context) error {
		called = true
		if got != ctx {
			t.Errorf("preparation context = %v, want original context %v", got, ctx)
		}
		return nil
	}
	if err := prepareWorkspaceWithStatus(ctx, reporter, phase, prepare); err != nil {
		t.Fatalf("prepareWorkspaceWithStatus() error = %v", err)
	}
	if !called {
		t.Fatal("preparation was not called")
	}
	if memoryReporter, ok := reporter.(*status.MemoryReporter); ok {
		if events := memoryReporter.Events(); len(events) != 0 {
			t.Fatalf("direct preparation reported status events: %+v", events)
		}
	}
}

func TestPrepareWorkspaceWithStatusReportsSuccess(t *testing.T) {
	reporter := status.NewMemoryReporter()
	called := false
	err := prepareWorkspaceWithStatus(
		context.Background(), reporter, status.PhaseResettingWorkspace,
		func(ctx context.Context) error {
			called = true
			if status.OperationID(ctx) == "" {
				t.Fatal("preparation context has no status operation ID")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("prepareWorkspaceWithStatus() error = %v", err)
	}
	if !called {
		t.Fatal("preparation was not called")
	}
	assertWorkspaceStatusEvents(
		t, reporter.Events(), status.PhaseResettingWorkspace, status.StateSucceeded,
	)
}

func TestPrepareWorkspaceWithStatusReportsFailure(t *testing.T) {
	reporter := status.NewMemoryReporter()
	prepareErr := errors.New("source preparation failed")
	err := prepareWorkspaceWithStatus(
		context.Background(), reporter, status.PhaseRebuildingWorkspace,
		func(context.Context) error { return prepareErr },
	)
	if !errors.Is(err, prepareErr) {
		t.Fatalf("prepareWorkspaceWithStatus() error = %v, want %v", err, prepareErr)
	}
	assertWorkspaceStatusEvents(
		t, reporter.Events(), status.PhaseRebuildingWorkspace, status.StateFailed,
	)
}

func TestPrepareWorkspaceKeepsInitializationErrorAndResetPrecedence(t *testing.T) {
	initErrInfo := &provider.AgentWorkspaceInfo{
		Workspace: &provider.Workspace{}, ContentFolder: "invalid\x00folder",
	}
	reporter := status.NewMemoryReporter()
	if err := prepareWorkspace(context.Background(), prepareWorkspaceParams{
		workspaceInfo: initErrInfo,
		reporter:      reporter,
	}); err == nil {
		t.Fatal("prepareWorkspace() returned nil for invalid content folder")
	}
	if events := reporter.Events(); len(events) != 0 {
		t.Fatalf("initialization error reported status events: %+v", events)
	}

	contentFolder := t.TempDir()
	if err := os.MkdirAll(contentFolder, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	workspaceInfo := &provider.AgentWorkspaceInfo{
		Workspace: &provider.Workspace{
			Source: provider.WorkspaceSource{Container: "container"},
		},
		ContentFolder: contentFolder,
	}
	workspaceInfo.CLIOptions.Reset = true
	workspaceInfo.CLIOptions.Recreate = true
	if err := prepareWorkspace(context.Background(), prepareWorkspaceParams{
		workspaceInfo: workspaceInfo,
		logger:        workspaceTestLogger{},
		reporter:      reporter,
	}); err != nil {
		t.Fatalf("prepareWorkspace() error = %v", err)
	}
	assertWorkspaceStatusEvents(
		t, reporter.Events(), status.PhaseResettingWorkspace, status.StateSucceeded,
	)
}

func TestPrepareWorkspaceRewritesPlatformLocalContentFolder(t *testing.T) {
	origin := t.TempDir()
	expectedContentFolder := filepath.Join(origin, "content")
	if err := os.MkdirAll(expectedContentFolder, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	workspaceInfo := &provider.AgentWorkspaceInfo{
		Workspace: &provider.Workspace{
			Source: provider.WorkspaceSource{LocalFolder: t.TempDir()},
		},
		ContentFolder: filepath.Join(t.TempDir(), "original-content"),
		Origin:        origin,
	}
	workspaceInfo.CLIOptions.Platform.Enabled = true
	if err := prepareWorkspace(context.Background(), prepareWorkspaceParams{
		workspaceInfo: workspaceInfo,
		logger:        workspaceTestLogger{},
	}); err != nil {
		t.Fatalf("prepareWorkspace() error = %v", err)
	}
	if workspaceInfo.ContentFolder != expectedContentFolder {
		t.Errorf(
			"ContentFolder = %q, want platform content folder %q",
			workspaceInfo.ContentFolder, expectedContentFolder,
		)
	}
}

func assertWorkspaceStatusEvents(
	t *testing.T,
	events []status.Event,
	phase status.Phase,
	finalState status.State,
) {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("status events = %+v, want started and final", events)
	}
	if events[0].Phase != phase || events[0].State != status.StateStarted {
		t.Errorf(
			"start event = %+v, want phase %q and state %q",
			events[0], phase, status.StateStarted,
		)
	}
	if events[1].Phase != phase || events[1].State != finalState {
		t.Errorf(
			"final event = %+v, want phase %q and state %q",
			events[1], phase, finalState,
		)
	}
}

func TestWaitForDockerPropagatesResolvedPath(t *testing.T) {
	const resolvedPath = "/Users/dev/.rd/bin/docker"

	for _, configuredPath := range []string{"", "docker"} {
		t.Run(configuredPath, func(t *testing.T) {
			workspaceInfo := &provider.AgentWorkspaceInfo{}
			workspaceInfo.Agent.Docker.Path = configuredPath
			initializer := &workspaceInitializer{workspaceInfo: workspaceInfo}
			resultChan := make(chan dockerInstallResult, 1)
			resultChan <- dockerInstallResult{path: resolvedPath}

			if err := initializer.waitForDocker(resultChan); err != nil {
				t.Fatalf("waitForDocker() error = %v", err)
			}
			if got := workspaceInfo.Agent.Docker.Path; got != resolvedPath {
				t.Fatalf("Docker.Path = %q, want %q", got, resolvedPath)
			}
		})
	}
}

func TestWaitForDockerPreservesExplicitPath(t *testing.T) {
	const customPath = "/opt/custom/bin/docker"
	workspaceInfo := &provider.AgentWorkspaceInfo{}
	workspaceInfo.Agent.Docker.Path = customPath
	initializer := &workspaceInitializer{workspaceInfo: workspaceInfo}
	resultChan := make(chan dockerInstallResult, 1)
	resultChan <- dockerInstallResult{path: "/Users/dev/.rd/bin/docker"}

	if err := initializer.waitForDocker(resultChan); err != nil {
		t.Fatalf("waitForDocker() error = %v", err)
	}
	if got := workspaceInfo.Agent.Docker.Path; got != customPath {
		t.Fatalf("Docker.Path = %q, want explicit path %q", got, customPath)
	}
}

func TestWaitForDockerReturnsDiscoveryError(t *testing.T) {
	discoveryErr := errors.New("docker not found")
	initializer := &workspaceInitializer{workspaceInfo: &provider.AgentWorkspaceInfo{}}
	resultChan := make(chan dockerInstallResult, 1)
	resultChan <- dockerInstallResult{err: discoveryErr}

	if err := initializer.waitForDocker(resultChan); !errors.Is(err, discoveryErr) {
		t.Fatalf("waitForDocker() error = %v, want wrapped %v", err, discoveryErr)
	}
}
