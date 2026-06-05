package workspace

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

// Shared fixture values reused across command tests.
const (
	testWorkspaceName = "node-js"
	testContext       = "default"
	testProvider      = "docker"
	testIDE           = "vscode"
	testGitRepo       = "github.com/acme/node-js"
	testGitBranch     = "main"
	testLocalFolder   = "/home/me/project"
	formatPlain       = "plain"
	formatJSON        = "json"
)

// fakeWorkspaceClient is a minimal BaseWorkspaceClient used to exercise
// command Run methods without a real provider.
type fakeWorkspaceClient struct {
	workspace string
	context   string
	provider  string
	config    *provider.Workspace
	status    client.Status
	statusErr error
}

func (f *fakeWorkspaceClient) Provider() string { return f.provider }
func (f *fakeWorkspaceClient) Context() string  { return f.context }
func (f *fakeWorkspaceClient) RefreshOptions(context.Context, []string, bool) error {
	return nil
}

func (f *fakeWorkspaceClient) Status(context.Context, client.StatusOptions) (client.Status, error) {
	return f.status, f.statusErr
}
func (f *fakeWorkspaceClient) Stop(context.Context, client.StopOptions) error     { return nil }
func (f *fakeWorkspaceClient) Delete(context.Context, client.DeleteOptions) error { return nil }

func (f *fakeWorkspaceClient) Workspace() string { return f.workspace }

func (f *fakeWorkspaceClient) WorkspaceConfig() *provider.Workspace { return f.config }
func (f *fakeWorkspaceClient) Lock(context.Context) error           { return nil }
func (f *fakeWorkspaceClient) Unlock()                              {}

// captureStdout runs fn while redirecting os.Stdout to a pipe and returns
// everything written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}
