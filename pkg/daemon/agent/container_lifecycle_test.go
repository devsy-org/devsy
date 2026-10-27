package agent

import (
	"testing"
	"time"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWorkdir = "/workspaces/app"

func TestResolveInactivityTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   TimeoutInputs
		want time.Duration
	}{
		{name: "empty disables", in: TimeoutInputs{}},
		{name: "zero disables", in: TimeoutInputs{Canonical: "0"}},
		{name: "zero duration disables", in: TimeoutInputs{Canonical: "0s"}},
		{name: "positive", in: TimeoutInputs{Canonical: "10m"}, want: 10 * time.Minute},
		{name: "legacy alone", in: TimeoutInputs{Legacy: "5m"}, want: 5 * time.Minute},
		{
			name: "equal aliases",
			in:   TimeoutInputs{Canonical: "1m", Legacy: "60s"},
			want: time.Minute,
		},
		{
			name: "shutdown none disables",
			in:   TimeoutInputs{Canonical: "1m", ShutdownAction: config.ShutdownActionNone},
		},
		{name: "disabled daemon disables", in: TimeoutInputs{Canonical: "1m", DisableDaemon: true}},
		{
			name: "stopContainer enabled",
			in: TimeoutInputs{
				Canonical:      "1m",
				ShutdownAction: config.ShutdownActionStopContainer,
			},
			want: time.Minute,
		},
		{
			name: "stopCompose without timeout is fine",
			in:   TimeoutInputs{ShutdownAction: config.ShutdownActionStopCompose},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveInactivityTimeout(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveInactivityTimeoutRejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		in      TimeoutInputs
		wantErr string
	}{
		{"malformed", TimeoutInputs{Canonical: "soon"}, "agent.containerInactivityTimeout"},
		{"malformed legacy", TimeoutInputs{Legacy: "soon"}, "agent.kubernetes.inactivityTimeout"},
		{"negative", TimeoutInputs{Canonical: "-1m"}, "must not be negative"},
		{
			"conflicting aliases",
			TimeoutInputs{Canonical: "1m", Legacy: "2m"},
			"conflicts with deprecated alias",
		},
		{
			"stopCompose rejected",
			TimeoutInputs{Canonical: "1m", ShutdownAction: config.ShutdownActionStopCompose},
			"not supported by the kubernetes driver",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveInactivityTimeout(tt.in)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestBuildWorkspaceDaemonConfig_ManagedWithoutPlatform(t *testing.T) {
	merged := &config.MergedDevContainerConfig{}
	merged.ShutdownAction = config.ShutdownActionStopContainer
	merged.RemoteUser = "vscode"

	cfg, err := BuildWorkspaceDaemonConfig(
		devsy.PlatformOptions{},
		&provider2.Workspace{},
		&config.SubstitutionContext{ContainerWorkspaceFolder: testWorkdir},
		merged,
		LifecycleOptions{Managed: true, Timeout: time.Minute},
	)
	require.NoError(t, err)

	assert.Equal(t, "1m0s", cfg.Timeout)
	assert.Equal(t, config.ShutdownActionStopContainer, cfg.ShutdownAction)
	assert.True(t, cfg.Lifecycle.Managed())
	assert.True(t, cfg.Lifecycle.SetupArmsTimer)
	assert.Empty(t, cfg.Ssh, "a timeout-only config must not request the embedded SSH server")
	assert.Empty(t, cfg.Platform.AccessKey)
}

func TestBuildWorkspaceDaemonConfig_PlatformKeepsNetworking(t *testing.T) {
	merged := &config.MergedDevContainerConfig{}
	merged.ShutdownAction = config.ShutdownActionNone

	cfg, err := BuildWorkspaceDaemonConfig(
		devsy.PlatformOptions{AccessKey: "key", PlatformHost: "p", WorkspaceHost: "w"},
		&provider2.Workspace{},
		&config.SubstitutionContext{ContainerWorkspaceFolder: testWorkdir},
		merged,
		LifecycleOptions{},
	)
	require.NoError(t, err)

	assert.Equal(t, "key", cfg.Platform.AccessKey)
	assert.Equal(t, SshConfig{Workdir: testWorkdir, User: "root"}, cfg.Ssh)
	assert.Equal(t, config.ShutdownActionNone, cfg.ShutdownAction)
	assert.False(t, cfg.Lifecycle.Managed())
}

func TestRefreshLifecycleKeepsPlatformAndReplacesPolicy(t *testing.T) {
	stored, err := EncodeDaemonConfig(&DaemonConfig{
		Platform:       devsy.PlatformOptions{AccessKey: "key"},
		Ssh:            SshConfig{User: "root"},
		Timeout:        "5m",
		ShutdownAction: config.ShutdownActionStopContainer,
	})
	require.NoError(t, err)

	refreshed, err := RefreshLifecycle(
		stored, LifecycleOptions{Managed: true, Timeout: 2 * time.Minute},
		config.ShutdownActionStopContainer,
	)
	require.NoError(t, err)
	cfg, err := DecodeDaemonConfig(refreshed)
	require.NoError(t, err)

	assert.Equal(t, "2m0s", cfg.Timeout)
	assert.True(t, cfg.Lifecycle.Managed())
	assert.Equal(t, "key", cfg.Platform.AccessKey)
	assert.Equal(t, "root", cfg.Ssh.User)
	assert.Equal(t, config.ShutdownActionStopContainer, cfg.ShutdownAction)
}

func TestRefreshLifecycleKeepsZeroTimeoutManaged(t *testing.T) {
	refreshed, err := RefreshLifecycle("", LifecycleOptions{Managed: true}, "")
	require.NoError(t, err)
	cfg, err := DecodeDaemonConfig(refreshed)
	require.NoError(t, err)

	assert.Empty(t, cfg.Timeout)
	assert.True(t, cfg.Lifecycle.Managed())
	assert.Empty(t, cfg.Ssh)
}
