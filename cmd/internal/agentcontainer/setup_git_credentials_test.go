//go:build !windows

package agentcontainer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/git"
	"github.com/stretchr/testify/require"
)

func TestGitCredentialsWithDeletedWorkingDirectory(t *testing.T) {
	const childEnv = "DEVSY_TEST_GIT_DELETED_CWD"
	if os.Getenv(childEnv) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// #nosec G204 -- re-executes the current test binary from os.Executable.
		cmd := exec.CommandContext(
			ctx,
			binary,
			"-test.run=^TestGitCredentialsWithDeletedWorkingDirectory$",
		)
		cmd.Env = append(os.Environ(), childEnv+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	home := t.TempDir()
	systemConfig := filepath.Join(home, "system.gitconfig")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_SYSTEM", systemConfig)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")

	gitConfig := git.At("/").Config()
	const existingHelper = "!existing-helper [a-z].*"
	require.NoError(t, gitConfig.Add(ctx, "credential.helper", existingHelper, git.ScopeSystem))
	require.NoError(t, gitConfig.Add(ctx, "credential.helper", existingHelper, git.ScopeGlobal))

	// Only the child changes CWD, leaving the rest of the package tests isolated.
	deletedDir := filepath.Join(home, "deleted-cwd")
	require.NoError(t, os.Mkdir(deletedDir, 0o700))
	require.NoError(t, os.Chdir(deletedDir))
	require.NoError(t, os.Remove(deletedDir))
	if err := git.At("").Config().Add(ctx, "test.key", "value", git.ScopeSystem); err == nil {
		t.Log("this Git version accepts an inherited deleted working directory")
	} else {
		t.Logf("inherited deleted working directory: %v", err)
	}

	t.Run("system", func(t *testing.T) {
		verifySystemGitCredentialCleanup(t, ctx, gitConfig, existingHelper)
	})
	t.Run("global fallback", func(t *testing.T) {
		verifyGlobalGitCredentialCleanup(t, ctx, existingHelper)
	})
}

func verifySystemGitCredentialCleanup(
	t *testing.T,
	ctx context.Context,
	gitConfig *git.Config,
	existingHelper string,
) {
	t.Helper()
	cleanup, err := configureSystemGitCredentials(ctx, nil, "")
	require.NoError(t, err)
	values, err := gitConfig.GetAll(ctx, "credential.helper", git.ScopeSystem)
	require.NoError(t, err)
	require.Len(t, values, 2)
	require.Equal(t, existingHelper, values[0])
	cleanup()
	values, err = gitConfig.GetAll(ctx, "credential.helper", git.ScopeSystem)
	require.NoError(t, err)
	require.Equal(t, []string{existingHelper}, values)
}

func verifyGlobalGitCredentialCleanup(t *testing.T, ctx context.Context, existingHelper string) {
	t.Helper()
	// Inject only the system write failure; the fallback runs real Git commands.
	deniedSystem := git.At("/", git.WithRunner(permissionDeniedGitRunner{t: t})).Config()
	const temporaryHelper = "!temporary-helper [a-z].*"
	globalConfig, scope, err := addGitCredentialHelper(ctx, deniedSystem, temporaryHelper, "")
	require.NoError(t, err)
	require.Equal(t, git.ScopeGlobal, scope)
	values, err := globalConfig.GetAll(ctx, "credential.helper", scope)
	require.NoError(t, err)
	require.Equal(t, []string{existingHelper, temporaryHelper}, values)
	require.NoError(
		t,
		globalConfig.UnsetValue(ctx, "credential.helper", temporaryHelper, scope),
	)
	values, err = globalConfig.GetAll(ctx, "credential.helper", scope)
	require.NoError(t, err)
	require.Equal(t, []string{existingHelper}, values)
}

type permissionDeniedGitRunner struct {
	t *testing.T
}

func (r permissionDeniedGitRunner) Run(
	_ context.Context,
	options git.RunOptions,
) (git.RunResult, error) {
	r.t.Helper()
	require.Equal(r.t, "--system", options.Args[1])
	return git.RunResult{}, &git.CommandError{Stderr: "Permission denied", ExitCode: 1}
}
