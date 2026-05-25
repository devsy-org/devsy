package features

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfoTagsCmd_RequiresExactlyOneArg(t *testing.T) {
	cmd := NewInfoTagsCmd(nil)
	assert.NotNil(t, cmd.Args)
	assert.Error(t, cmd.Args(cmd, nil))
	assert.NoError(t, cmd.Args(cmd, []string{"ghcr.io/devcontainers/features/go"}))
	assert.Error(t, cmd.Args(cmd, []string{"first", "second"}))
}

func TestInfoTagsCmd_InvalidFeatureReference(t *testing.T) {
	cmd := &InfoTagsCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: "plain"}}
	err := cmd.Run("not a valid reference!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature reference")
}

func TestInfoTagsCmd_CommandMetadata(t *testing.T) {
	cmd := NewInfoTagsCmd(nil)
	assert.Equal(t, "tags <feature-id>", cmd.Use)
	assert.Contains(t, cmd.Short, "tags")
	assert.NotEmpty(t, cmd.Long)
}
