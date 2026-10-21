package workspace

import (
	"context"
	"io"
	"os"
	"strings"
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
	deleted   bool
}

func (f *fakeWorkspaceClient) Provider() string { return f.provider }
func (f *fakeWorkspaceClient) Context() string  { return f.context }
func (f *fakeWorkspaceClient) RefreshOptions(context.Context, []string, bool) error {
	return nil
}

func (f *fakeWorkspaceClient) Status(context.Context, client.StatusOptions) (client.Status, error) {
	return f.status, f.statusErr
}
func (f *fakeWorkspaceClient) Stop(context.Context, client.StopOptions) error { return nil }
func (f *fakeWorkspaceClient) Delete(context.Context, client.DeleteOptions) error {
	f.deleted = true
	return nil
}

func (f *fakeWorkspaceClient) Workspace() string { return f.workspace }

func (f *fakeWorkspaceClient) WorkspaceConfig() *provider.Workspace { return f.config }
func (f *fakeWorkspaceClient) Lock(context.Context) error           { return nil }
func (f *fakeWorkspaceClient) Unlock()                              {}

// captureStdout runs fn while redirecting os.Stdout to a temporary file and
// returns everything written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	capture, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = capture
	defer func() {
		os.Stdout = orig
		require.NoError(t, capture.Close())
	}()

	fn()

	_, err = capture.Seek(0, io.SeekStart)
	require.NoError(t, err)
	out, err := io.ReadAll(capture)
	require.NoError(t, err)
	return string(out)
}

func TestCaptureStdoutLargeOutput(t *testing.T) {
	want := strings.Repeat("0123456789abcdef", 1<<16)
	orig := os.Stdout
	got := captureStdout(t, func() {
		n, err := io.WriteString(os.Stdout, want)
		require.NoError(t, err)
		require.Equal(t, len(want), n)
	})
	require.Equal(t, want, got)
	require.Same(t, orig, os.Stdout)
}
