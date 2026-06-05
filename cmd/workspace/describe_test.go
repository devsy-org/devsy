package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
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
