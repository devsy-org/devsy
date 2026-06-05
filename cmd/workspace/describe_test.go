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
				GitRepository: "github.com/acme/node-js",
				GitBranch:     "main",
			},
			want: "git:github.com/acme/node-js@main",
		},
		{
			name: "git commit when no branch",
			src: provider.WorkspaceSource{
				GitRepository: "github.com/acme/node-js",
				GitCommit:     "abc123",
			},
			want: "git:github.com/acme/node-js@abc123",
		},
		{
			name: "git pr when no branch or commit",
			src: provider.WorkspaceSource{
				GitRepository:  "github.com/acme/node-js",
				GitPRReference: "refs/pull/42/head",
			},
			want: "git:github.com/acme/node-js@refs/pull/42/head",
		},
		{
			name: "git repo only",
			src:  provider.WorkspaceSource{GitRepository: "github.com/acme/node-js"},
			want: "git:github.com/acme/node-js",
		},
		{
			name: "git with subpath",
			src: provider.WorkspaceSource{
				GitRepository: "github.com/acme/node-js",
				GitBranch:     "main",
				GitSubPath:    "services/api",
			},
			want: "git:github.com/acme/node-js@main (services/api)",
		},
		{
			name: "local folder",
			src:  provider.WorkspaceSource{LocalFolder: "/home/me/project"},
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
		ID:       "node-js",
		Context:  "default",
		Provider: provider.WorkspaceProviderConfig{Name: "docker"},
		IDE:      provider.WorkspaceIDEConfig{Name: "vscode"},
		Source: provider.WorkspaceSource{
			GitRepository: "github.com/acme/node-js",
			GitBranch:     "main",
		},
		Machine: provider.WorkspaceMachineConfig{ID: "node-js"},
	}
	cmd := &DescribeCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: "json"}}
	fake := &fakeWorkspaceClient{workspace: "node-js", config: cfg, status: client.StatusRunning}

	out := captureStdout(t, func() {
		require.NoError(t, cmd.Run(t.Context(), fake))
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "node-js", got["id"])
	assert.Equal(t, "default", got["context"])
	assert.Equal(t, string(client.StatusRunning), got["state"])
	provBlock, ok := got["provider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "docker", provBlock["name"])
}
