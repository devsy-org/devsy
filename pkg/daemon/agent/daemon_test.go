package agent

import (
	"errors"
	"testing"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testStateRoot = "/state"

func TestBuildDaemonArgs(t *testing.T) {
	args := buildDaemonArgs("/usr/bin/devsy", InstallOptions{
		StateLocation:  StateLocation{Root: testStateRoot, Layout: StateLayoutCanonical},
		Interval:       "30s",
		ShutdownAction: config.ShutdownActionStopContainer,
	})

	assert.Equal(t, []string{
		"/usr/bin/devsy", "internal", "agent", "daemon",
		"--state-root", testStateRoot, "--state-layout", "canonical",
		"--diagnostics-reader-uid", "0", "--diagnostics-reader-gid", "0",
		"--interval", "30s", "--shutdown-action", config.ShutdownActionStopContainer,
	}, args)
}

func TestFallbackRuntimeLockPath(t *testing.T) {
	cacheDir := func() (string, error) { return "/home/devsy/.cache", nil }

	rootPath, err := fallbackRuntimeLockPath(0, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, "/run/devsy/agent-daemon.lock", rootPath)

	userPath, err := fallbackRuntimeLockPath(1000, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, "/home/devsy/.cache/devsy/agent-daemon.lock", userPath)

	_, err = fallbackRuntimeLockPath(1000, func() (string, error) {
		return "", errors.New("unavailable")
	})
	require.ErrorContains(t, err, "get user cache directory for daemon lock")
}

func TestDaemonUnitStateLocationMatchesExactArgumentValues(t *testing.T) {
	unit := systemdUnitContents(
		"/usr/bin/devsy internal agent daemon --state-root /state/development --state-layout canonical",
	)
	location, configured, err := daemonUnitStateLocation(unit)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(
		t,
		StateLocation{Root: "/state/development", Layout: StateLayoutCanonical},
		location,
	)
	assert.NotEqual(t, StateLocation{Root: "/state/dev", Layout: StateLayoutCanonical}, location)
}

func TestDaemonUnitStateLocationSupportsQuotedRoot(t *testing.T) {
	unit := systemdUnitContents(
		`/usr/bin/devsy internal agent daemon --state-root "/state/dev sy" --state-layout agent-home`,
	)
	location, configured, err := daemonUnitStateLocation(unit)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, StateLocation{Root: "/state/dev sy", Layout: StateLayoutAgentHome}, location)
}

func TestBuildWorkspaceDaemonConfig_ShutdownAction(t *testing.T) {
	tests := []struct {
		name           string
		shutdownAction string
		want           string
	}{
		{
			name:           "passes through empty value from merged config",
			shutdownAction: "",
			want:           "",
		},
		{
			name:           "preserves none",
			shutdownAction: config.ShutdownActionNone,
			want:           config.ShutdownActionNone,
		},
		{
			name:           "preserves stopContainer",
			shutdownAction: config.ShutdownActionStopContainer,
			want:           config.ShutdownActionStopContainer,
		},
		{
			name:           "preserves stopCompose",
			shutdownAction: config.ShutdownActionStopCompose,
			want:           config.ShutdownActionStopCompose,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := &config.MergedDevContainerConfig{}
			merged.ShutdownAction = tt.shutdownAction

			cfg, err := BuildWorkspaceDaemonConfig(
				devsy.PlatformOptions{},
				&provider2.Workspace{},
				&config.SubstitutionContext{},
				merged,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ShutdownAction != tt.want {
				t.Errorf("ShutdownAction = %q, want %q", cfg.ShutdownAction, tt.want)
			}
		})
	}
}
