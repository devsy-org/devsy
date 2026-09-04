package up

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

// fakeWorkspaceClient is a minimal client2.BaseWorkspaceClient used to
// exercise prepareResolvedWorkspaceSecrets without a real provider.
type fakeWorkspaceClient struct {
	config *provider.Workspace
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
func (f *fakeWorkspaceClient) Stop(context.Context, client2.StopOptions) error     { return nil }
func (f *fakeWorkspaceClient) Delete(context.Context, client2.DeleteOptions) error { return nil }

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

// newTestProjectWorkspace writes a .devsy/config.yaml declaring a repository-
// owned SOPS source plus an attached secret, and the matching encrypted
// document, under a fresh temp directory. It returns a fake client whose
// WorkspaceConfig().Source.LocalFolder points at that directory, mirroring
// what workspace2.Resolve produces for a local-folder positional argument.
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

// TestPrepareResolvedWorkspaceSecrets_DiscoversLocalProjectSecrets is a
// regression test for a bug where repository-owned SOPS secrets declared in
// a local project's .devsy/config.yaml were never discovered for ordinary
// `devsy up <path>` CLI invocations: project discovery ran against the
// source returned by parseWorkspaceSource (populated only by --source/
// --from-snapshot), not the workspace source workspace2.Resolve derives
// from a positional argument.
func TestPrepareResolvedWorkspaceSecrets_DiscoversLocalProjectSecrets(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY", testProjectSecretsAgeIdentity)
	client := newTestProjectWorkspace(t)

	cmd := &UpCmd{}
	err := cmd.prepareResolvedWorkspaceSecrets(context.Background(), testConfig(), client)
	require.NoError(t, err)

	require.Contains(t, cmd.SecretsEnv, "SOPS_E2E_SECRET="+testProjectSecretsPlaintext)
}
