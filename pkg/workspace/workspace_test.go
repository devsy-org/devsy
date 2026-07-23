package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProjectPath       = "/home/user/project"
	testProviderName      = "test-provider"
	testWorkspaceID       = "project"
	testDevContainerImage = "ghcr.io/example/image:latest"
	testDevContainerPath  = ".devcontainer/devcontainer.json"
)

func TestValidateDesiredID(t *testing.T) {
	cases := []struct {
		name      string
		desiredID string
		wantErr   bool
	}{
		{name: "empty is always valid", desiredID: "", wantErr: false},
		{name: "lowercase letters, digits and dashes", desiredID: "my-workspace-1", wantErr: false},
		{
			name:      "max length is allowed",
			desiredID: strings.Repeat("a", maxWorkspaceIDLength),
			wantErr:   false,
		},
		{name: "uppercase is rejected", desiredID: "MyWorkspace", wantErr: true},
		{name: "spaces are rejected", desiredID: "my workspace", wantErr: true},
		{name: "underscores are rejected", desiredID: "my_workspace", wantErr: true},
		{
			name:      "too long is rejected",
			desiredID: strings.Repeat("a", maxWorkspaceIDLength+1),
			wantErr:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateDesiredID(c.desiredID)
			if c.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateDesiredID_InvalidCharsCheckedBeforeLength(t *testing.T) {
	// An over-length id that also contains invalid characters should report the
	// character rule first, matching the historical ordering.
	err := validateDesiredID(strings.Repeat("A", maxWorkspaceIDLength+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lowercase letters")
}

func TestEnsureWorkspaceID(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		workspaceID string
		want        string
	}{
		{name: "no args and no id yields empty", args: nil, workspaceID: "", want: ""},
		{
			name:        "explicit id passes through",
			args:        nil,
			workspaceID: "explicit-ws",
			want:        "explicit-ws",
		},
		{
			name:        "explicit id wins over args",
			args:        []string{"github.com/foo/bar"},
			workspaceID: "keep",
			want:        "keep",
		},
		{
			name:        "derives id from first arg",
			args:        []string{"github.com/foo/Bar"},
			workspaceID: "",
			want:        ToID("github.com/foo/Bar"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ensureWorkspaceID(c.args, c.workspaceID))
		})
	}
}

func TestResolveWorkspaceSource_ExplicitSourceWins(t *testing.T) {
	src := &providerpkg.WorkspaceSource{Image: "ubuntu:22.04"}
	got, picture := resolveWorkspaceSource(context.Background(), resolveWorkspaceConfigParams{
		source: src,
		// deliberately set fields that must be ignored when an explicit source is present
		name:        "github.com/foo/bar",
		isLocalPath: true,
	})
	assert.Equal(t, *src, got)
	assert.Empty(t, picture)
}

func TestResolveWorkspaceSource_LocalFolder(t *testing.T) {
	got, picture := resolveWorkspaceSource(context.Background(), resolveWorkspaceConfigParams{
		name:        testProjectPath,
		isLocalPath: true,
	})
	assert.Equal(t, providerpkg.WorkspaceSource{LocalFolder: testProjectPath}, got)
	assert.Empty(t, picture)
}

func TestResolveWorkspaceConfig_LocalFolderMetadata(t *testing.T) {
	provider := &ProviderWithOptions{Config: &providerpkg.ProviderConfig{Name: testProviderName}}
	devsyConfig := &config.Config{DefaultContext: testDefaultContext}

	ws := resolveWorkspaceConfig(
		context.Background(),
		provider,
		devsyConfig,
		resolveWorkspaceConfigParams{
			name:                 testProjectPath,
			workspaceID:          testWorkspaceID,
			isLocalPath:          true,
			uid:                  "fixed-uid",
			sshConfigPath:        "/tmp/ssh_config",
			sshConfigIncludePath: "/tmp/ssh_include",
		},
	)

	assert.Equal(t, testWorkspaceID, ws.ID)
	assert.Equal(t, "fixed-uid", ws.UID, "explicitly provided uid should be preserved")
	assert.Equal(t, testDefaultContext, ws.Context)
	assert.Equal(t, testProviderName, ws.Provider.Name)
	assert.Equal(t, testProjectPath, ws.Source.LocalFolder)
	assert.Equal(t, "/tmp/ssh_config", ws.SSHConfigPath)
	assert.Equal(t, "/tmp/ssh_include", ws.SSHConfigIncludePath)
	assert.Equal(t, ws.CreationTimestamp, ws.LastUsedTimestamp,
		"creation and last-used timestamps should match on a fresh workspace")
}

func TestResolveWorkspaceConfig_GeneratesUIDWhenEmpty(t *testing.T) {
	provider := &ProviderWithOptions{Config: &providerpkg.ProviderConfig{Name: testProviderName}}
	devsyConfig := &config.Config{DefaultContext: testDefaultContext}

	ws := resolveWorkspaceConfig(
		context.Background(),
		provider,
		devsyConfig,
		resolveWorkspaceConfigParams{
			name:        testProjectPath,
			workspaceID: testWorkspaceID,
			isLocalPath: true,
		},
	)
	assert.NotEmpty(t, ws.UID, "a UID should be generated when none is provided")
}

func TestApplyDevContainerOverrides_NoOp(t *testing.T) {
	// With no overrides and no container source, nothing is persisted, so this
	// must not touch the filesystem or error.
	ws := &providerpkg.Workspace{ID: "ws", Context: testDefaultContext}
	require.NoError(t, applyDevContainerOverrides(ws, ResolveParams{}))
	assert.Empty(t, ws.DevContainerImage)
	assert.Empty(t, ws.DevContainerPath)
}

func TestApplyDevContainerOverrides_SetsImageAndPath(t *testing.T) {
	setupTestPathManager(t)

	ws := &providerpkg.Workspace{ID: "ws-overrides", Context: testDefaultContext}
	err := applyDevContainerOverrides(ws, ResolveParams{
		DevContainerImage: testDevContainerImage,
		DevContainerPath:  testDevContainerPath,
	})
	require.NoError(t, err)

	assert.Equal(t, testDevContainerImage, ws.DevContainerImage)
	assert.Equal(t, testDevContainerPath, ws.DevContainerPath)

	// The overrides must have been persisted to disk.
	loaded, err := providerpkg.LoadWorkspaceConfig(testDefaultContext, ws.ID)
	require.NoError(t, err)
	assert.Equal(t, testDevContainerImage, loaded.DevContainerImage)
	assert.Equal(t, testDevContainerPath, loaded.DevContainerPath)
}
