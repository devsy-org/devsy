package agentworkspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/types"
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

const (
	dockerPolicyDisabled = "false"
	dockerPolicyEnabled  = "true"
	dockerPolicyInvalid  = "invalid"
	testInstalledDocker  = "/installed/docker"
)

type dockerInstallationCase struct {
	name      string
	path      string
	policy    string
	existing  bool
	wantCalls int
	wantPath  string
	wantError string
}

func TestDockerInstallationPolicy(t *testing.T) {
	cases := []dockerInstallationCase{
		{
			name:      "missing disabled",
			policy:    dockerPolicyDisabled,
			wantError: `docker CLI is unavailable and agent.docker.install is false (executable "docker")`,
		},
		{name: "missing unset", wantCalls: 1, wantPath: testInstalledDocker},
		{
			name:      "missing enabled",
			policy:    dockerPolicyEnabled,
			wantCalls: 1,
			wantPath:  testInstalledDocker,
		},
		{name: "existing disabled", policy: dockerPolicyDisabled, existing: true},
		{
			name:      "custom missing disabled",
			path:      "missing-custom",
			policy:    dockerPolicyDisabled,
			wantError: "custom docker path",
		},
		{name: "custom missing unset", path: "missing-custom", wantError: "custom docker path"},
		{
			name:     "existing custom disabled",
			path:     "custom-docker",
			policy:   dockerPolicyDisabled,
			existing: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertDockerInstallationPolicy(t, tc)
		})
	}
}

func assertDockerInstallationPolicy(t *testing.T, tc dockerInstallationCase) {
	t.Helper()
	if runtime.GOOS == "darwin" && tc.wantCalls > 0 {
		tc.wantCalls = 0
		tc.wantPath = ""
		tc.wantError = "not found"
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if tc.existing {
		writeDockerFixture(t, bin, tc.path)
	}
	info := &provider.AgentWorkspaceInfo{}
	info.Agent.Docker.Path = tc.path
	info.Agent.Docker.Install = types.StrBool(tc.policy)
	calls := 0
	initializer := &workspaceInitializer{
		workspaceInfo: info,
		findDarwinDocker: func() (string, error) {
			return "", errors.New("not found")
		},
		installDocker: func(context.Context) (string, error) {
			calls++
			return testInstalledDocker, nil
		},
	}
	path, err := initializer.ensureDockerInstalled(context.Background())
	if tc.wantError == "" {
		assert.NoError(t, err)
	} else {
		assert.ErrorContains(t, err, tc.wantError)
	}
	assert.Equal(t, tc.wantCalls, calls)
	assert.Equal(t, tc.wantPath, path)
}

func writeDockerFixture(t *testing.T, bin, configuredPath string) {
	t.Helper()
	name := configuredPath
	if name == "" {
		name = docker
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	path := filepath.Join(bin, name)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(executable, path); err == nil {
		return
	}
	copyDockerFixture(t, executable, path)
}

func copyDockerFixture(t *testing.T, executable, path string) {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(executable))
	if err != nil {
		t.Fatal(err)
	}
	source, err := root.Open(filepath.Base(executable))
	if err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	info, err := source.Stat()
	if err != nil {
		_ = source.Close()
		_ = root.Close()
		t.Fatal(err)
	}
	targetRoot, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		_ = source.Close()
		_ = root.Close()
		t.Fatal(err)
	}
	target, err := targetRoot.OpenFile(
		filepath.Base(path),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		info.Mode().Perm()&0o700,
	)
	if err != nil {
		_ = source.Close()
		_ = root.Close()
		_ = targetRoot.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	if err := errors.Join(
		copyErr,
		source.Close(),
		target.Close(),
		root.Close(),
		targetRoot.Close(),
	); err != nil {
		t.Fatal(err)
	}
}

func TestDockerInstallDisabledTriState(t *testing.T) {
	for _, tc := range []struct {
		policy   types.StrBool
		disabled bool
	}{
		{policy: "", disabled: false},
		{policy: dockerPolicyEnabled, disabled: false},
		{policy: dockerPolicyDisabled, disabled: true},
	} {
		info := &provider.AgentWorkspaceInfo{}
		info.Agent.Docker.Install = tc.policy
		initializer := &workspaceInitializer{workspaceInfo: info}
		if got := initializer.isDockerInstallDisabled(); got != tc.disabled {
			t.Errorf("install %q disabled = %t, want %t", tc.policy, got, tc.disabled)
		}
	}
}

func TestDockerManagementPolicy(t *testing.T) {
	for _, driver := range []string{provider.DockerDriver, provider.KubernetesDriver} {
		for _, local := range []string{dockerPolicyDisabled, dockerPolicyEnabled} {
			for _, policy := range []string{"", dockerPolicyEnabled, dockerPolicyDisabled, dockerPolicyInvalid} {
				t.Run(driver+"/"+local+"/"+policy, func(t *testing.T) {
					assertDockerManagementPolicy(t, driver, local, policy)
				})
			}
		}
	}
}

func assertDockerManagementPolicy(t *testing.T, driver, local, policy string) {
	t.Helper()
	info := &provider.AgentWorkspaceInfo{}
	info.Agent.Driver = driver
	info.Agent.Local = types.StrBool(local)
	info.Agent.Docker.ConfigureDaemon = types.StrBool(policy)
	configureCalls := 0
	initializer := &workspaceInitializer{
		workspaceInfo: info,
		configureDocker: func(context.Context) error {
			configureCalls++
			return nil
		},
	}
	err := initializer.validateDockerManagement()
	if policy == dockerPolicyInvalid {
		assert.ErrorContains(t, err, "agent.docker.configureDaemon")
	} else {
		assert.NoError(t, err)
	}
	initializer.tryConfigureDockerDaemon(context.Background())
	want := 0
	if driver == provider.DockerDriver && local == dockerPolicyDisabled &&
		policy != dockerPolicyDisabled && policy != dockerPolicyInvalid {
		want = 1
	}
	assert.Equal(t, want, configureCalls)
}

func TestInvalidDockerManagementFailsBeforeInitialization(t *testing.T) {
	for _, field := range []string{"install", "configureDaemon"} {
		t.Run(field, func(t *testing.T) {
			info := &provider.AgentWorkspaceInfo{}
			if field == "install" {
				info.Agent.Docker.Install = dockerPolicyInvalid
			} else {
				info.Agent.Docker.ConfigureDaemon = dockerPolicyInvalid
			}
			calls := 0
			initializer := &workspaceInitializer{
				workspaceInfo: info,
				installDocker: func(context.Context) (string, error) {
					calls++
					return "", nil
				},
				configureDocker: func(context.Context) error {
					calls++
					return nil
				},
			}
			if err := initializer.initialize(
				context.Background(),
			); err == nil ||
				!strings.Contains(err.Error(), field) {
				t.Errorf("initialize() error = %v, want invalid %s error", err, field)
			}
			if calls != 0 {
				t.Errorf("invalid policy triggered %d Docker management calls", calls)
			}
		})
	}
}
