package up

import (
	"context"
	"os"
	"path/filepath"
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
	// Synthetic test-only identity generated solely for this fixture. gitleaks:allow.
	testProjectSecretsAgeIdentity = "AGE-SECRET-KEY-12UWYSAH2MRDQ5K4EWC4253PDTCSCS32Y5EFQ8TEN2SL3QYU2GN2SG88CZX"
	testProjectSecretsPlaintext   = "SUPER_SECRET_TEST_VALUE_7B91"
	testProjectEncryptedFixture   = "testdata/sops-project-secrets.enc.yaml"
)

func newTestProjectWorkspace(t *testing.T) *fakeWorkspaceClient {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".devsy"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".devsy", "config.yaml"),
		[]byte("secretSources:\n"+
			"  - name: project\n"+
			"    type: sops\n"+
			"    path: secrets.enc.yaml\n"+
			"secrets:\n"+
			"  - sops:project/SOPS_E2E_SECRET\n"),
		0o600,
	))
	fixture, err := os.ReadFile(testProjectEncryptedFixture)
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
	t.Setenv("SOPS_AGE_KEY", testProjectSecretsAgeIdentity)
	client := newTestProjectWorkspace(t)

	cmd := &UpCmd{}
	err := cmd.prepareResolvedWorkspaceSecrets(context.Background(), testConfig(), client)
	require.NoError(t, err)

	require.Contains(t, cmd.SecretsEnv, "SOPS_E2E_SECRET="+testProjectSecretsPlaintext)
}

func newFailingProjectWorkspace(t *testing.T) *fakeWorkspaceClient {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".devsy"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".devsy", "config.yaml"),
		[]byte("secretSources:\n"+
			"  - name: project\n"+
			"    type: sops\n"+
			"    path: nonexistent.enc.yaml\n"+
			"secrets:\n"+
			"  - sops:project/MISSING\n"),
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
