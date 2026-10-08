//go:build linux || darwin || unix

package setup

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"syscall"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

func TestSetupWorkspaceOwnershipVanishedSSHAgentDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "devsy-ssh-agent-deleted")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "listener.sock"), nil, 0o600))
	require.NoError(t, os.RemoveAll(dir))
	_, statErr := os.Lstat(dir)
	require.ErrorIs(t, statErr, fs.ErrNotExist)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(dir, "listener.sock"))
	t.Setenv(pkgconfig.EnvWorkspaceID, "")

	_, err := user.Lookup("nobody")
	require.NoError(t, err)
	cfg := &ContainerSetupConfig{SetupInfo: &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{RemoteUser: "nobody"},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: filepath.Join(parent, "not-created"),
		},
	}}
	require.NoError(t, setupWorkspaceOwnership(cfg))
	require.NoError(t, setupWorkspaceOwnership(cfg))
	t.Setenv("SSH_AUTH_SOCK", "")
	require.NoError(t, setupWorkspaceOwnership(cfg))
}

func TestChownAgentSockMissingDirectoryIsNonfatal(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "already-removed", "listener.sock"))
	require.NoError(t, chownAgentSock(&config.Result{}))
}

func TestChownAgentSockWithoutEnvironment(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	// No socket means no user lookup is needed either.
	result := &config.Result{MergedConfig: &config.MergedDevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser: "devsy-1423-missing-user",
		},
	}}
	require.NoError(t, chownAgentSock(result))
}

func TestChownAgentSockActiveDirectory(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)
	dir := t.TempDir()
	// #nosec G302 -- owner needs directory traversal; the test asserts this mode is preserved.
	require.NoError(t, os.Chmod(dir, 0o700))
	socketPath := filepath.Join(dir, "listener.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("active socket placeholder"), 0o600))
	t.Setenv("SSH_AUTH_SOCK", socketPath)
	t.Setenv(pkgconfig.EnvWorkspaceID, "")
	cfg := &ContainerSetupConfig{SetupInfo: &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{RemoteUser: current.Username},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: filepath.Join(dir, "not-created"),
		},
	}}
	// Avoid linkRootHome's /home/root side effects when tests run as root.
	require.NoError(t, chownAgentSock(cfg.SetupInfo))
	if current.Username != "root" {
		require.NoError(t, setupWorkspaceOwnership(cfg))
	}
	// #nosec G304 -- socketPath is confined to this test's temporary directory.
	contents, err := os.ReadFile(socketPath)
	require.NoError(t, err)
	require.Equal(t, "active socket placeholder", string(contents))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestSetupWorkspaceOwnershipUnknownSSHAgentUser(t *testing.T) {
	const missingUser = "devsy-1423-missing-user"
	_, err := user.Lookup(missingUser)
	var unknownUser user.UnknownUserError
	require.ErrorAs(t, err, &unknownUser)

	parent := t.TempDir()
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(parent, "already-removed", "listener.sock"))
	t.Setenv(pkgconfig.EnvWorkspaceID, "")
	cfg := &ContainerSetupConfig{SetupInfo: &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{RemoteUser: missingUser},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: filepath.Join(parent, "not-created"),
		},
	}}
	err = setupWorkspaceOwnership(cfg)
	require.ErrorContains(t, err, "chown ssh agent sock file: lookup user:")
	require.ErrorAs(t, err, &unknownUser)
}

func TestSetupWorkspaceOwnershipInvalidSSHAgentDirectory(t *testing.T) {
	_, err := user.Lookup("nobody")
	require.NoError(t, err)
	parent := t.TempDir()
	file := filepath.Join(parent, "not-a-directory")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(file, "agent", "listener.sock"))
	t.Setenv(pkgconfig.EnvWorkspaceID, "")
	cfg := &ContainerSetupConfig{SetupInfo: &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{RemoteUser: "nobody"},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: filepath.Join(parent, "not-created"),
		},
	}}
	err = setupWorkspaceOwnership(cfg)
	require.ErrorContains(t, err, "chown ssh agent sock file:")
	require.ErrorIs(t, err, syscall.ENOTDIR)
}
