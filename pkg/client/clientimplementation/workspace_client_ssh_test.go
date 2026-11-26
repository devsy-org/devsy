package clientimplementation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sshDeleteWorkspaceID = "sshdel"
	sshDeleteUID         = "uid-sshdel"
	sshDeleteHost        = sshDeleteWorkspaceID + ".devsy"
)

func prepareSSHDeleteFixture(t *testing.T) string {
	t.Helper()

	useTempDevsyHome(t, t.TempDir())
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)

	return filepath.Join(userHome, ".ssh", "config")
}

func saveSSHDeleteWorkspace(t *testing.T) {
	t.Helper()

	require.NoError(t, provider.SaveWorkspaceConfig(&provider.Workspace{
		ID:      sshDeleteWorkspaceID,
		UID:     sshDeleteUID,
		Context: testLocksContext,
	}))
}

func configureSSHDeleteBlock(t *testing.T, cfgPath, uid string) {
	t.Helper()

	owner, err := ssh.NewOwner(testLocksContext, uid)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o700))
	require.NoError(t, ssh.ConfigureSSHConfig(ssh.SSHConfigParams{
		SSHConfigPath: cfgPath,
		Context:       testLocksContext,
		Workspace:     sshDeleteWorkspaceID,
		User:          "dev",
		Owner:         owner,
	}))
}

func readSSHDeleteConfig(t *testing.T, cfgPath string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(cfgPath))
	require.NoError(t, err)

	return string(data)
}

func TestDeleteWorkspaceFolderRemovesOwnedSSHBlock(t *testing.T) {
	cfgPath := prepareSSHDeleteFixture(t)
	saveSSHDeleteWorkspace(t)
	configureSSHDeleteBlock(t, cfgPath, sshDeleteUID)

	require.NoError(t, DeleteWorkspaceFolder(DeleteWorkspaceFolderParams{
		Context:       testLocksContext,
		WorkspaceID:   sshDeleteWorkspaceID,
		SSHConfigPath: cfgPath,
	}))

	assert.NotContains(t, readSSHDeleteConfig(t, cfgPath), ssh.MarkerStartPrefix)
}

func TestDeleteWorkspaceFolderKeepsForeignOwnedSSHBlock(t *testing.T) {
	cfgPath := prepareSSHDeleteFixture(t)
	saveSSHDeleteWorkspace(t)
	configureSSHDeleteBlock(t, cfgPath, "uid-recreated")
	before := readSSHDeleteConfig(t, cfgPath)

	require.NoError(t, DeleteWorkspaceFolder(DeleteWorkspaceFolderParams{
		Context:       testLocksContext,
		WorkspaceID:   sshDeleteWorkspaceID,
		SSHConfigPath: cfgPath,
	}))

	assert.Equal(t, before, readSSHDeleteConfig(t, cfgPath))
	assert.Contains(t, before, ssh.MarkerStartPrefix+sshDeleteHost)
}
