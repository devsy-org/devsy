package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRegistry = "ghcr.io"
	testVersion  = "1.0.0"
)

func TestPublishCmd_FlagDefaults(t *testing.T) {
	cmd := NewPublishCmd(nil)

	targetFlag := cmd.Flags().Lookup(names.Target)
	require.NotNil(t, targetFlag)
	assert.Equal(t, "", targetFlag.DefValue)

	registryFlag := cmd.Flags().Lookup(names.Registry)
	require.NotNil(t, registryFlag)
	assert.Equal(t, testRegistry, registryFlag.DefValue)

	namespaceFlag := cmd.Flags().Lookup(names.Namespace)
	require.NotNil(t, namespaceFlag)
	assert.Equal(t, "", namespaceFlag.DefValue)
}

func TestPublishCmd_AllFlagsRegistered(t *testing.T) {
	cmd := NewPublishCmd(nil)
	expected := []string{"target", "registry", "namespace"}
	for _, name := range expected {
		assert.NotNil(t, cmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
}

func TestPublishCmd_TargetRequired(t *testing.T) {
	cmd := NewPublishCmd(nil)
	flag := cmd.Flags().Lookup(names.Target)
	require.NotNil(t, flag)

	annotations := flag.Annotations
	require.Contains(t, annotations, "cobra_annotation_bash_completion_one_required_flag")
}

func TestValidatePublishTarget_NotADirectory(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("hello"), 0o600))

	_, err := validatePublishTarget(tmpFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target must be a directory")
}

func TestValidatePublishTarget_MissingMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := validatePublishTarget(tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse feature metadata")
}

func TestValidatePublishTarget_MissingID(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "devcontainer-feature.json"),
		[]byte(`{"name": "My Feature", "version": "1.0.0"}`),
		0o600,
	))

	_, err := validatePublishTarget(tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required property \"id\"")
}

func TestValidatePublishTarget_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "devcontainer-feature.json"),
		[]byte(`{"id": "go", "version": "1.0.0", "name": "Go"}`),
		0o600,
	))

	cfg, err := validatePublishTarget(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "go", cfg.ID)
	assert.Equal(t, testVersion, cfg.Version)
}

func TestValidatePublishTarget_NonexistentPath(t *testing.T) {
	_, err := validatePublishTarget("/nonexistent/path/that/does/not/exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat target")
}
