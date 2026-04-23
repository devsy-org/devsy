//go:build !windows

package container

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressSetupInfoPreservesSubstitutedValues(t *testing.T) {
	// Simulate post-substitution state: PATH is a real value, not a
	// ${containerEnv:PATH} literal.
	info := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteEnv: map[string]string{
					"PATH": "/usr/local/bin:/usr/bin:/bin",
					"HOME": "/home/testuser",
				},
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	compressed, err := compressSetupInfo(info)
	require.NoError(t, err)
	require.NotEmpty(t, compressed)

	// Round-trip: decompress and unmarshal.
	decompressed, err := compress.Decompress(compressed)
	require.NoError(t, err)

	var roundTripped config.Result
	require.NoError(t, json.Unmarshal([]byte(decompressed), &roundTripped))

	// The resolved PATH must come through, not a literal variable reference.
	gotPath := roundTripped.MergedConfig.RemoteEnv["PATH"]
	assert.Equal(t, "/usr/local/bin:/usr/bin:/bin", gotPath)
	assert.False(t, strings.Contains(gotPath, "${containerEnv:"),
		"PATH should be resolved, not contain ${containerEnv:} literals")
	assert.Equal(t, "/home/testuser", roundTripped.MergedConfig.RemoteEnv["HOME"])
}
