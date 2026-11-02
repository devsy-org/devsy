package devcontainer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

type failingArchitectureDriver struct {
	mockDriver
	err error
}

func (d *failingArchitectureDriver) TargetArchitecture(context.Context, string) (string, error) {
	return "", d.err
}

func TestAttemptPreStartDeliveryLogsPlannedPostStartPhase(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	r := newTestRunner(&mockDriver{})

	// A nil configuration would fail if pre-start options were built. A
	// post-start strategy should be recognized before reaching that work.
	r.attemptPreStartDelivery(context.Background(), nil, nil, nil)

	if got := logs.FilterMessage("agent delivery scheduled for post-start").Len(); got != 1 {
		t.Fatalf("planned post-start diagnostic count = %d, want 1", got)
	}
	if got := logs.FilterMessage("strategy phase is post-start, not pre-start").Len(); got != 0 {
		t.Fatalf("phase-mismatch error diagnostic count = %d, want 0", got)
	}
}

func TestAttemptPreStartDeliveryLogsFailureAndContinues(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	failure := errors.New("architecture unavailable")
	r := newTestRunner(&failingArchitectureDriver{err: failure})
	r.workspaceConfig.Agent.Driver = provider.DockerDriver

	r.attemptPreStartDelivery(
		context.Background(),
		&config.MergedDevContainerConfig{},
		&resolveParams{substitutionContext: &config.SubstitutionContext{}},
		&config.BuildInfo{ImageMetadata: &config.ImageMetadataConfig{}},
	)

	entries := logs.FilterMessage(
		"pre-start agent delivery failed; will use post-start: " +
			"resolve target architecture: architecture unavailable",
	).All()
	if len(entries) != 1 {
		t.Fatalf("pre-start delivery failure diagnostic count = %d, want 1", len(entries))
	}
	if len(logs.FilterMessage("agent delivery scheduled for post-start").All()) != 0 {
		t.Fatal("pre-start failure was misreported as a planned post-start strategy")
	}
}

func TestResolveNewContainerPreStartDelivery(t *testing.T) {
	const baseImageName = "alpine"
	if runtime.GOOS == goosWindows {
		t.Skip("requires a shell executable")
	}

	for _, tc := range []struct {
		name     string
		fail     bool
		mount    bool
		rawMount bool
	}{
		{name: "success"},
		{name: "failure", fail: true},
		{name: "occupied mount", mount: true},
		{name: "raw mount", rawMount: true},
	} {
		script := "#!/bin/sh\ncase \"$1\" in\nvolume) exit 0 ;;\n" +
			"run) printf '%s\\n' \"$TEST_AGENT_VERSION\" ;;\n*) exit 1 ;;\nesac\n"
		if tc.fail {
			script = "#!/bin/sh\nexit 1\n"
		}
		t.Run(tc.name, func(t *testing.T) {
			r, d := newPreStartTestRunner(t, script)

			p := recreateResolveParams()
			p.options.Recreate = false
			p.parsedConfig.Config.Image = baseImageName
			p.parsedConfig.Config.ContainerEnv = map[string]string{"USER_VALUE": "preserved"}
			if tc.mount {
				p.parsedConfig.Config.Mounts = []*config.Mount{{
					Type: "volume", Source: "user-data", Target: "/opt/devsy",
				}}
			}
			if tc.rawMount {
				p.parsedConfig.Config.RunArgs = []string{"--volume=user-data:/opt/devsy"}
			}
			p.parsedConfig.Raw = config.CloneDevContainerConfig(p.parsedConfig.Config)
			p.substitutionContext = &config.SubstitutionContext{}

			resolved, err := r.resolveNewContainer(context.Background(), p)
			require.NoError(t, err)
			require.NotNil(t, d.params)
			assert.Equal(t, "preserved", d.params.Options.Env["USER_VALUE"])
			assert.NotContains(t, resolved.mergedConfig.ContainerEnv, pkgconfig.EnvAgentPath)
			assertPreStartDeliveryOptions(t, d.params.Options, r.id, tc.name)
		})
	}
}

func newPreStartTestRunner(t *testing.T, script string) (*runner, *imageOnlyRuntime) {
	t.Helper()
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	require.NoError(t, os.WriteFile(docker, []byte(script), 0o600))
	chmod := exec.Command("chmod", "u+x", "docker")
	chmod.Dir = dir
	require.NoError(t, chmod.Run())
	d := &imageOnlyRuntime{mockDriver: mockDriver{findResult: runningContainerDetails()}}
	r := newTestRunner(d)
	r.workspaceConfig.Agent.Driver = provider.DockerDriver
	r.workspaceConfig.Agent.Docker.Path = docker
	r.workspaceConfig.Agent.Docker.Env = map[string]string{
		"TEST_AGENT_VERSION": version.GetVersion(),
	}
	r.workspaceConfig.Workspace = &provider.Workspace{ID: r.id, UID: composeUpTestWorkspaceUID}
	r.imageBackend = &separateImages{details: &config.ImageDetails{ID: overlayImageField}}
	r.reporter = status.Nop()
	return r, d
}

func assertPreStartDeliveryOptions(t *testing.T, options *driver.RunOptions, id, scenario string) {
	t.Helper()
	if scenario != "success" {
		assert.NotContains(t, options.Env, pkgconfig.EnvAgentPath)
		if scenario == "occupied mount" {
			require.Len(t, options.Mounts, 1)
			assert.Equal(t, "user-data", options.Mounts[0].Source)
		} else {
			assert.Empty(t, options.Mounts)
		}
		return
	}
	assert.Equal(t, "/opt/devsy/devsy", options.Env[pkgconfig.EnvAgentPath])
	require.Len(t, options.Mounts, 1)
	assert.Equal(t, "devsy-agent-"+id, options.Mounts[0].Source)
	assert.Equal(t, "/opt/devsy", options.Mounts[0].Target)
	assert.Equal(t, "volume", options.Mounts[0].Type)
}
