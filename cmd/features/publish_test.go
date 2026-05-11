package features

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishCmd_FlagDefaults(t *testing.T) {
	cmd := NewPublishCmd(nil)

	targetFlag := cmd.Flags().Lookup("target")
	require.NotNil(t, targetFlag)
	assert.Equal(t, "", targetFlag.DefValue)

	registryFlag := cmd.Flags().Lookup("registry")
	require.NotNil(t, registryFlag)
	assert.Equal(t, "ghcr.io", registryFlag.DefValue)

	namespaceFlag := cmd.Flags().Lookup("namespace")
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
	flag := cmd.Flags().Lookup("target")
	require.NotNil(t, flag)

	annotations := flag.Annotations
	require.Contains(t, annotations, "cobra_annotation_bash_completion_one_required_flag")
}

func TestBuildPublishReference(t *testing.T) {
	tests := []struct {
		name      string
		registry  string
		namespace string
		id        string
		version   string
		want      string
	}{
		{
			name:      "with namespace",
			registry:  "ghcr.io",
			namespace: "devcontainers/features",
			id:        "go",
			version:   "1.0.0",
			want:      "ghcr.io/devcontainers/features/go:1.0.0",
		},
		{
			name:     "without namespace",
			registry: "ghcr.io",
			id:       "go",
			version:  "1.0.0",
			want:     "ghcr.io/go:1.0.0",
		},
		{
			name:      "custom registry",
			registry:  "registry.example.com",
			namespace: "my-org/features",
			id:        "node",
			version:   "2.0.0",
			want:      "registry.example.com/my-org/features/node:2.0.0",
		},
		{
			name:      "latest version",
			registry:  "ghcr.io",
			namespace: "test",
			id:        "python",
			version:   "latest",
			want:      "ghcr.io/test/python:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPublishReference(tt.registry, tt.namespace, tt.id, tt.version)
			assert.Equal(t, tt.want, got)
		})
	}
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
	assert.Contains(t, err.Error(), "missing required 'id' field")
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
	assert.Equal(t, "1.0.0", cfg.Version)
}

func TestValidatePublishTarget_NonexistentPath(t *testing.T) {
	_, err := validatePublishTarget("/nonexistent/path/that/does/not/exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat target")
}

func TestBuildFeatureImage(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "devcontainer-feature.json"),
		[]byte(`{"id": "go", "version": "1.0.0"}`),
		0o600,
	))
	// #nosec G306 -- test install script must be executable
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "install.sh"),
		[]byte("#!/bin/bash\necho hello\n"),
		0o750,
	))

	img, err := buildFeatureImage(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, img)

	layers, err := img.Layers()
	require.NoError(t, err)
	assert.Len(t, layers, 1)
}

func TestParsePublishRef_Valid(t *testing.T) {
	ref, err := parsePublishRef("ghcr.io", "devcontainers/features", "go", "1.0.0")
	require.NoError(t, err)
	assert.Contains(t, ref.String(), "ghcr.io/devcontainers/features/go:1.0.0")
}

func TestParsePublishRef_Invalid(t *testing.T) {
	_, err := parsePublishRef("", "", "INVALID REF!!!", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse publish reference")
}
