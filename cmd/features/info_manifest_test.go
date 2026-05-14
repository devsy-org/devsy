package features

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfoManifestCmd_FlagDefaults(t *testing.T) {
	cmd := NewInfoManifestCmd(nil)

	outputFlag := cmd.Flags().Lookup("output")
	require.NotNil(t, outputFlag)
	assert.Equal(t, "json", outputFlag.DefValue)
}

func TestInfoManifestCmd_RequiresExactlyOneArg(t *testing.T) {
	cmd := NewInfoManifestCmd(nil)
	assert.NotNil(t, cmd.Args)

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "no args", args: []string{}, wantErr: true},
		{name: "one arg", args: []string{"ghcr.io/devcontainers/features/go"}, wantErr: false},
		{name: "two args", args: []string{"a", "b"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cmd.Args(cmd, tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInfoManifestCmd_InvalidOutputFormat(t *testing.T) {
	cmd := &InfoManifestCmd{Output: "xml"}
	err := cmd.Run("ghcr.io/devcontainers/features/go:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output format")
}

func TestInfoManifestCmd_InvalidFeatureReference(t *testing.T) {
	cmd := &InfoManifestCmd{Output: outputJSON}
	err := cmd.Run("not a valid reference!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature reference")
}

func TestInfoManifestCmd_CommandMetadata(t *testing.T) {
	cmd := NewInfoManifestCmd(nil)
	assert.Equal(t, "manifest <feature-id>", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
}
