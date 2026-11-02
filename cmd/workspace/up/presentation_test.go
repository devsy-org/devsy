package up

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDetachFlag   = "--detach"
	testDisplayLabel = "My Label"
	testShortAlias   = "short"
	testWorkspaceAPI = "api"
)

func TestDetachedArgsKeepPresentationFlags(t *testing.T) {
	got := detachedArgs([]string{
		testRepoArg,
		testDetachFlag,
		names.Flag(names.DisplayName), testDisplayLabel,
		names.FlagValue(names.SSHHostAlias, testShortAlias),
	})

	assert.Equal(t, []string{
		testRepoArg,
		names.Flag(names.DisplayName), testDisplayLabel,
		names.FlagValue(names.SSHHostAlias, testShortAlias),
	}, got)
}

func TestUpCmdRegistersPresentationFlags(t *testing.T) {
	cmd := NewUpCmd(&flags.GlobalFlags{})

	require.NoError(t, cmd.ParseFlags([]string{
		names.Flag(names.DisplayName), testDisplayLabel,
		names.Flag(names.SSHHostAlias), testShortAlias,
	}))
	display, err := cmd.Flags().GetString(names.DisplayName)
	require.NoError(t, err)
	alias, err := cmd.Flags().GetString(names.SSHHostAlias)
	require.NoError(t, err)
	assert.Equal(t, testDisplayLabel, display)
	assert.Equal(t, testShortAlias, alias)
}

func TestCheckPresentationSkipsWhenUnsetOrExisting(t *testing.T) {
	cfg := &config.Config{}

	unset := &UpCmd{}
	require.NoError(t, unset.checkPresentation(cfg, "ws", false))

	existing := &UpCmd{DisplayName: "Label", SSHHostAlias: testShortAlias}
	require.NoError(t, existing.checkPresentation(cfg, "ws", true))
	require.NoError(t, existing.checkPresentation(cfg, "", false))
}

func TestTargetWorkspaceID(t *testing.T) {
	cmd := &UpCmd{}
	assert.Empty(t, cmd.targetWorkspaceID(nil))

	cmd.ID = "explicit"
	assert.Equal(t, "explicit", cmd.targetWorkspaceID([]string{"other"}))
}

func setupUpTestHome(t *testing.T) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func TestCheckNewWorkspaceIDRejectsExistingAlias(t *testing.T) {
	setupUpTestHome(t)
	cfg := &config.Config{DefaultContext: defaultValue}
	sshConfig := filepath.Join(t.TempDir(), "config")
	require.NoError(t, provider.SaveWorkspaceConfig(&provider.Workspace{
		ID:           "web",
		UID:          "uid-web",
		Context:      defaultValue,
		SSHHostAlias: "backend",
	}))

	cmd := &UpCmd{}
	cmd.SSHConfigPath = sshConfig

	err := cmd.checkNewWorkspaceID(cfg, "backend", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend.devsy")
	assert.Contains(t, err.Error(), `"web"`)

	require.NoError(t, cmd.checkNewWorkspaceID(cfg, "backend", true))
	require.NoError(t, cmd.checkNewWorkspaceID(cfg, "", false))
	require.NoError(t, cmd.checkNewWorkspaceID(cfg, "fresh", false))
}

func TestValidateTargetChecksNewIDWithoutPresentationFlags(t *testing.T) {
	setupUpTestHome(t)
	cfg := &config.Config{DefaultContext: defaultValue}
	sshConfig := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(
		sshConfig, []byte("Host taken.devsy\n  HostName example.com\n"), 0o600,
	))

	cmd := &UpCmd{}
	cmd.SSHConfigPath = sshConfig

	err := cmd.validateTarget(cfg, "taken", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "taken.devsy")
	require.NoError(t, cmd.validateTarget(cfg, "taken", true))
}

type presentationClient struct {
	client2.BaseWorkspaceClient
	ws *provider.Workspace
}

func (c presentationClient) Context() string                      { return c.ws.Context }
func (c presentationClient) Workspace() string                    { return c.ws.ID }
func (c presentationClient) WorkspaceConfig() *provider.Workspace { return c.ws }

func TestSSHPresentationUsesCommittedAlias(t *testing.T) {
	setupUpTestHome(t)
	stored := &provider.Workspace{ID: testWorkspaceAPI, UID: "uid-api", Context: defaultValue}
	require.NoError(t, provider.SaveWorkspaceConfig(stored))

	inMemory := &provider.Workspace{ID: testWorkspaceAPI, UID: "uid-api", Context: defaultValue}
	client := presentationClient{ws: inMemory}
	assert.Equal(t, "api.devsy", sshPresentation(client))

	committed := *stored
	committed.SSHHostAlias = testShortAlias
	require.NoError(t, provider.SaveWorkspacePresentation(&committed))
	assert.Equal(t, "short.devsy", sshPresentation(client))

	recreated := &provider.Workspace{ID: testWorkspaceAPI, UID: "other-uid", Context: defaultValue}
	assert.Equal(t, "api.devsy", sshPresentation(presentationClient{ws: recreated}))
}

func TestSSHPresentationNilWorkspace(t *testing.T) {
	assert.Empty(t, sshPresentation(&fakeWorkspaceClient{}))
}

func TestValidateResolvedIDRejectsDerivedIDMatchingAlias(t *testing.T) {
	setupUpTestHome(t)
	cfg := &config.Config{DefaultContext: defaultValue}
	require.NoError(t, provider.SaveWorkspaceConfig(&provider.Workspace{
		ID:           "web",
		UID:          "uid-web",
		Context:      defaultValue,
		SSHHostAlias: "derived",
	}))
	cmd := &UpCmd{}
	cmd.SSHConfigPath = filepath.Join(t.TempDir(), "config")

	derived := &provider.Workspace{ID: "derived", UID: "uid-new", Context: defaultValue}
	err := cmd.validateResolvedID(cfg, presentationClient{ws: derived})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "derived.devsy")

	free := &provider.Workspace{ID: "free", UID: "uid-free", Context: defaultValue}
	require.NoError(t, cmd.validateResolvedID(cfg, presentationClient{ws: free}))
}
