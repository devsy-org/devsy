package up

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workspaceFolderCase struct {
	name           string
	resolvedFolder string
	configFolder   string
	subpath        string
	override       string
	noMergedConfig bool
	want           string
}

func TestUpResultWorkspaceFolder(t *testing.T) {
	const (
		defaultFolder  = "/workspaces/project"
		customFolder   = "/src/project"
		overrideFolder = "/override/project"
		gitSubpath     = "app"
	)
	tests := []workspaceFolderCase{
		{name: "default folder", resolvedFolder: defaultFolder, want: defaultFolder},
		{name: "custom mount folder", resolvedFolder: customFolder, want: customFolder},
		{
			name:           "no merged config",
			resolvedFolder: defaultFolder,
			noMergedConfig: true,
			want:           defaultFolder,
		},
		{
			name:           "explicit folder",
			resolvedFolder: defaultFolder,
			configFolder:   customFolder,
			want:           customFolder,
		},
		{
			name:           "git subpath",
			resolvedFolder: defaultFolder,
			subpath:        gitSubpath,
			want:           defaultFolder + "/app",
		},
		{
			name: "git subpath with explicit folder", resolvedFolder: customFolder,
			configFolder: customFolder, subpath: gitSubpath, want: customFolder + "/app",
		},
		{
			name:           "CLI override",
			resolvedFolder: defaultFolder,
			override:       overrideFolder,
			want:           overrideFolder,
		},
		{
			name:           "CLI override takes precedence",
			resolvedFolder: customFolder,
			configFolder:   customFolder,
			subpath:        gitSubpath,
			override:       overrideFolder,
			want:           overrideFolder,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertUpResultFolder(t, tt)
		})
	}
}

func assertUpResultFolder(t *testing.T, test workspaceFolderCase) {
	t.Helper()
	result := &devcconfig.Result{
		SubstitutionContext: &devcconfig.SubstitutionContext{
			ContainerWorkspaceFolder: test.resolvedFolder,
		},
	}
	if !test.noMergedConfig {
		result.MergedConfig = &devcconfig.MergedDevContainerConfig{
			DevContainerConfigBase: devcconfig.DevContainerConfigBase{
				WorkspaceFolder: test.configFolder,
			},
		}
	}
	workspace := &provider.Workspace{
		Source: provider.WorkspaceSource{GitSubPath: test.subpath},
	}
	client := prepareWorkspaceClient{workspaceConfig: workspace}
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.WorkspaceFolder = test.override
	workdir := cmd.resolveWorkdir(result, client)

	var output bytes.Buffer
	emitUpResult(&workspaceContext{result: result, workdir: workdir}, "", &output)
	var envelope devcconfig.ResultEnvelope
	require.NoError(t, json.Unmarshal(output.Bytes(), &envelope))
	assert.Equal(t, "success", envelope.Outcome)
	assert.Equal(t, test.want, envelope.RemoteWorkspaceFolder)
}
