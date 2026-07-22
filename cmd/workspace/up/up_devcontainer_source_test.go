package up

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const srcNone = "none"

func TestResolveDevContainerSource_ID(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.DevContainerSource = "id:default"

	require.NoError(t, cmd.resolveDevContainerSource())
	assert.Equal(t, "default", cmd.DevContainerID)
	assert.Empty(t, cmd.DevContainerSource)
}

func TestResolveDevContainerSource_Path(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.DevContainerSource = ".devcontainer/custom.json"

	require.NoError(t, cmd.resolveDevContainerSource())
	assert.Equal(t, ".devcontainer/custom.json", cmd.DevContainerPath)
	assert.Empty(t, cmd.DevContainerSource)
}

func TestResolveDevContainerSource_ExternalPathPassThrough(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.DevContainerSource = "/abs/external/devcontainer.json"

	require.NoError(t, cmd.resolveDevContainerSource())
	assert.Equal(t, "/abs/external/devcontainer.json", cmd.DevContainerSource)
	assert.Empty(t, cmd.DevContainerPath)
	assert.Empty(t, cmd.DevContainerID)
}

func TestResolveDevContainerSource_NoneAndImagePassThrough(t *testing.T) {
	for _, spec := range []string{srcNone, "image:python"} {
		cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
		cmd.DevContainerSource = spec

		require.NoError(t, cmd.resolveDevContainerSource())
		// none/image specs stay for agent-side handling.
		assert.Equal(t, spec, cmd.DevContainerSource)
		assert.Empty(t, cmd.DevContainerID)
		assert.Empty(t, cmd.DevContainerPath)
	}
}

func TestResolveDevContainerSource_Invalid(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.DevContainerSource = "image:"

	require.Error(t, cmd.resolveDevContainerSource())
}
