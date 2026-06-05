package workspace

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribeSource(t *testing.T) { //nolint:funlen // table-driven test
	cases := []struct {
		name string
		src  provider.WorkspaceSource
		want string
	}{
		{
			name: "git branch",
			src: provider.WorkspaceSource{
				GitRepository: testGitRepo,
				GitBranch:     testGitBranch,
			},
			want: "git:github.com/acme/node-js@main",
		},
		{
			name: "git commit when no branch",
			src: provider.WorkspaceSource{
				GitRepository: testGitRepo,
				GitCommit:     "abc123",
			},
			want: "git:github.com/acme/node-js@abc123",
		},
		{
			name: "git pr when no branch or commit",
			src: provider.WorkspaceSource{
				GitRepository:  testGitRepo,
				GitPRReference: "refs/pull/42/head",
			},
			want: "git:github.com/acme/node-js@refs/pull/42/head",
		},
		{
			name: "git repo only",
			src:  provider.WorkspaceSource{GitRepository: testGitRepo},
			want: "git:github.com/acme/node-js",
		},
		{
			name: "git with subpath",
			src: provider.WorkspaceSource{
				GitRepository: testGitRepo,
				GitBranch:     testGitBranch,
				GitSubPath:    "services/api",
			},
			want: "git:github.com/acme/node-js@main (services/api)",
		},
		{
			name: "local folder",
			src:  provider.WorkspaceSource{LocalFolder: testLocalFolder},
			want: "/home/me/project",
		},
		{
			name: "image",
			src:  provider.WorkspaceSource{Image: "ghcr.io/acme/img:1.0"},
			want: "ghcr.io/acme/img:1.0",
		},
		{
			name: "container",
			src:  provider.WorkspaceSource{Container: "my-container"},
			want: "my-container",
		},
		{
			name: "empty",
			src:  provider.WorkspaceSource{},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, describeSource(tc.src))
		})
	}
}

func TestDescribeCmd_JSONOutput(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	cfg := &provider.Workspace{
		ID:       testWorkspaceName,
		Context:  testContext,
		Provider: provider.WorkspaceProviderConfig{Name: testProvider},
		IDE:      provider.WorkspaceIDEConfig{Name: testIDE},
		Source: provider.WorkspaceSource{
			GitRepository: testGitRepo,
			GitBranch:     testGitBranch,
		},
		Machine: provider.WorkspaceMachineConfig{ID: testWorkspaceName},
	}
	cmd := &DescribeCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatJSON}}
	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		config:    cfg,
		status:    client.StatusRunning,
	}

	out := captureStdout(t, func() {
		require.NoError(t, cmd.Run(t.Context(), fake))
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, testWorkspaceName, got["id"])
	assert.Equal(t, testContext, got["context"])
	assert.Equal(t, string(client.StatusRunning), got["state"])
	provBlock, ok := got["provider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, testProvider, provBlock["name"])
}

func TestDescribeCmd_PlainPrintsAtDefaultVerbosity(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	cfg := &provider.Workspace{
		ID:       testWorkspaceName,
		Context:  testContext,
		Provider: provider.WorkspaceProviderConfig{Name: testProvider},
		IDE:      provider.WorkspaceIDEConfig{Name: testIDE},
		Source: provider.WorkspaceSource{
			GitRepository: testGitRepo,
			GitBranch:     testGitBranch,
		},
		Machine: provider.WorkspaceMachineConfig{ID: testWorkspaceName},
	}
	cmd := &DescribeCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}
	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		config:    cfg,
		status:    client.StatusRunning,
	}

	out := captureStdout(t, func() {
		require.NoError(t, cmd.Run(t.Context(), fake))
	})

	assert.Contains(t, out, testWorkspaceName)
	assert.Contains(t, out, "Running")
	assert.Contains(t, out, testProvider)
	assert.Contains(t, out, testIDE)
	assert.Contains(t, out, "git:github.com/acme/node-js@main")
}

func TestDescribeCmd_PlainOmitsEmptyFields(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	cfg := &provider.Workspace{
		ID:     testWorkspaceName,
		Source: provider.WorkspaceSource{LocalFolder: testLocalFolder},
		// No IDE, no Machine, no Context.
	}
	cmd := &DescribeCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}
	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		config:    cfg,
		status:    client.StatusStopped,
	}

	out := captureStdout(t, func() {
		require.NoError(t, cmd.Run(t.Context(), fake))
	})

	assert.Contains(t, out, testLocalFolder)
	assert.NotContains(t, out, "IDE")
	assert.NotContains(t, out, "Machine")
}

func TestDescribeCmd_NilConfigErrors(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	cmd := &DescribeCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}
	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		config:    nil,
		status:    client.StatusRunning,
	}

	err := cmd.Run(t.Context(), fake)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no configuration")
}
