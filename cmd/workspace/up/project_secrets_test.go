package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
)

type fakeWorkspaceClient struct {
	config  *provider.Workspace
	deleted bool
}

func (f *fakeWorkspaceClient) Provider() string { return "" }
func (f *fakeWorkspaceClient) Context() string  { return "" }
func (f *fakeWorkspaceClient) RefreshOptions(context.Context, []string, bool) error {
	return nil
}

func (f *fakeWorkspaceClient) Status(
	context.Context,
	client2.StatusOptions,
) (client2.Status, error) {
	return client2.StatusRunning, nil
}
func (f *fakeWorkspaceClient) Stop(context.Context, client2.StopOptions) error { return nil }
func (f *fakeWorkspaceClient) Delete(context.Context, client2.DeleteOptions) error {
	f.deleted = true
	return nil
}

func (f *fakeWorkspaceClient) Workspace() string { return "" }

func (f *fakeWorkspaceClient) WorkspaceConfig() *provider.Workspace { return f.config }
func (f *fakeWorkspaceClient) Lock(context.Context) error           { return nil }
func (f *fakeWorkspaceClient) Unlock()                              {}

const (
	// Synthetic test-only identity generated solely for this fixture.
	testProjectAgeIdentity = "AGE-SECRET-KEY-12UWYSAH2MRDQ5K4EWC4253PDTCSCS32Y5EFQ8TEN2SL3QYU2GN2SG88CZX" // gitleaks:allow
	testProjectPlaintext   = "SUPER_SECRET_TEST_VALUE_7B91"
	testProjectFixture     = "testdata/sops-project-secrets.enc.yaml"
)

func newTestProjectWorkspace(t *testing.T) *fakeWorkspaceClient {
	t.Helper()
	root := t.TempDir()
	devcontainerDir := filepath.Join(root, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(devcontainerDir, "devcontainer.json"),
		[]byte(`{
  "customizations": {
    "devsy": {
      "secretSources": [
        {
          "name": "project",
          "type": "sops",
          "path": "secrets.enc.yaml"
        }
      ],
      "secrets": [
        "sops:project/SOPS_E2E_SECRET"
      ]
    }
  }
}`),
		0o600,
	))
	fixture, err := os.ReadFile(testProjectFixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile( // #nosec G703 -- t.TempDir()-derived path
		filepath.Join(root, "secrets.enc.yaml"),
		fixture,
		0o600,
	))

	return &fakeWorkspaceClient{
		config: &provider.Workspace{Source: provider.WorkspaceSource{LocalFolder: root}},
	}
}

func TestPrepareResolvedWorkspaceSecrets_DiscoversLocalProjectSecrets(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY", testProjectAgeIdentity)
	client := newTestProjectWorkspace(t)

	cmd := &UpCmd{}
	err := cmd.prepareResolvedWorkspaceSecrets(context.Background(), testConfig(), client)
	require.NoError(t, err)

	require.Contains(t, cmd.SecretsEnv, "SOPS_E2E_SECRET="+testProjectPlaintext)
}

func newFailingProjectWorkspace(t *testing.T) *fakeWorkspaceClient {
	t.Helper()
	root := t.TempDir()
	devcontainerDir := filepath.Join(root, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(devcontainerDir, "devcontainer.json"),
		[]byte(`{
  "customizations": {
    "devsy": {
      "secretSources": [
        {
          "name": "project",
          "type": "sops",
          "path": "nonexistent.enc.yaml"
        }
      ],
      "secrets": [
        "sops:project/MISSING"
      ]
    }
  }
}`),
		0o600,
	))
	return &fakeWorkspaceClient{
		config: &provider.Workspace{
			ID:     "my-test-workspace",
			Source: provider.WorkspaceSource{LocalFolder: root},
		},
	}
}

func TestPrepareClient_InteractiveResolutionPreservesExistingWorkspaceOnError(t *testing.T) {
	client := newFailingProjectWorkspace(t)
	cmd := &UpCmd{
		GlobalFlags: &flags.GlobalFlags{},
		resolveWorkspace: func(
			context.Context,
			*config.Config,
			workspace.ResolveParams,
		) (client2.BaseWorkspaceClient, error) {
			return client, nil
		},
	}

	_, err := cmd.prepareClient(context.Background(), testConfig(), nil)
	require.Error(t, err)
	require.False(
		t,
		client.deleted,
		"interactively resolved existing workspace must not be deleted on secret error",
	)
}

func TestPrepareClient_NewWorkspaceCleanedUpOnError(t *testing.T) {
	client := newFailingProjectWorkspace(t)
	cmd := &UpCmd{
		GlobalFlags: &flags.GlobalFlags{},
		resolveWorkspace: func(
			context.Context,
			*config.Config,
			workspace.ResolveParams,
		) (client2.BaseWorkspaceClient, error) {
			return client, nil
		},
	}

	_, err := cmd.prepareClient(
		context.Background(),
		testConfig(),
		[]string{filepath.Join(t.TempDir(), "new-workspace")},
	)
	require.Error(t, err)
	require.True(
		t,
		client.deleted,
		"newly created workspace must be cleaned up on secret error",
	)
}

func setupTestGitRepoWithDevContainer(
	t *testing.T,
	dir string,
) (func(args ...string) string, string) {
	t.Helper()
	runGit := func(args ...string) string {
		c := exec.Command(
			"git",
			args...) // #nosec G204 -- test runner helper with controlled arguments
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")

	devcontainerDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(devcontainerDir, "devcontainer.json"),
		[]byte(`{
  "customizations": {
    "devsy": {
      "secretSources": [
        {"name": "project", "type": "sops", "path": "secrets.enc.yaml"}
      ],
      "secrets": ["sops:project/SOPS_E2E_SECRET"]
    }
  }
}`),
		0o600,
	))
	fixture, err := os.ReadFile(testProjectFixture)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "secrets.enc.yaml"), fixture, 0o600),
	) // #nosec G703 -- test temp dir
	runGit("add", ".")
	runGit("commit", "-m", "initial commit with secrets")
	commitA := runGit("rev-parse", "HEAD")
	return runGit, commitA
}

func TestDiscoverRemoteProjectSecrets_PinsImmutableCommitSHA(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY", testProjectAgeIdentity)
	gitDir := t.TempDir()
	runGit, commitA := setupTestGitRepoWithDevContainer(t, gitDir)

	source := &provider.WorkspaceSource{
		GitRepository: gitDir,
		GitBranch:     "main",
	}

	cmd := &UpCmd{}
	projectCtx, err := cmd.discoverProjectSecrets(context.Background(), source)
	require.NoError(t, err)
	require.NotNil(t, projectCtx)

	// Verify immutable SHA pinning (99A.1)
	require.Equal(t, commitA, source.GitCommit, "inspected source must be pinned to commit A")

	// Advance the branch to commit B
	require.NoError(
		t,
		os.WriteFile(filepath.Join(gitDir, "dummy.txt"), []byte("advance branch"), 0o600),
	)
	runGit("add", "dummy.txt")
	runGit("commit", "-m", "advance branch to commit B")
	commitB := runGit("rev-parse", "HEAD")
	require.NotEqual(t, commitA, commitB)

	// Rediscover using the source that has GitCommit pinned to commitA
	projectCtx2, err := cmd.discoverProjectSecrets(context.Background(), source)
	require.NoError(t, err)
	require.NotNil(t, projectCtx2)
	require.Equal(t, commitA, source.GitCommit, "rediscovered source must remain pinned to commitA")
}

func TestDiscoverProjectSecrets_RejectsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	devcontainerDir := filepath.Join(root, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(devcontainerDir, "devcontainer.json"),
		[]byte(`{
  "customizations": {
    "devsy": {
      "secretSources": [
        {"name": "project", "type": "sops", "path": "/secrets.enc.yaml"}
      ],
      "secrets": ["sops:project/SOPS_E2E_SECRET"]
    }
  }
}`),
		0o600,
	))

	source := &provider.WorkspaceSource{LocalFolder: root}
	cmd := &UpCmd{}
	_, err := cmd.discoverProjectSecrets(context.Background(), source)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be relative to the repository root")
}
