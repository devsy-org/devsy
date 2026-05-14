package features

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfoTagsCmd_FlagDefaults(t *testing.T) {
	cmd := NewInfoTagsCmd(nil)

	outputFlag := cmd.Flags().Lookup("output")
	require.NotNil(t, outputFlag)
	assert.Equal(t, outputText, outputFlag.DefValue)
}

func TestInfoTagsCmd_RequiresExactlyOneArg(t *testing.T) {
	cmd := NewInfoTagsCmd(nil)
	assert.NotNil(t, cmd.Args)
	assert.Error(t, cmd.Args(cmd, nil))
	assert.NoError(t, cmd.Args(cmd, []string{"ghcr.io/devcontainers/features/go"}))
	assert.Error(t, cmd.Args(cmd, []string{"first", "second"}))
}

func TestInfoTagsCmd_InvalidOutputFormat(t *testing.T) {
	cmd := &InfoTagsCmd{Output: "csv"}
	err := cmd.Run("ghcr.io/devcontainers/features/go:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output format")
}

func TestInfoTagsCmd_InvalidFeatureReference(t *testing.T) {
	cmd := &InfoTagsCmd{Output: outputText}
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
