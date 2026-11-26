package image

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPublishRegistry = "ghcr.io"
	testPublishLatest   = "latest"
)

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
			registry:  testPublishRegistry,
			namespace: "devcontainers/features",
			id:        "go",
			version:   "1.0.0",
			want:      "ghcr.io/devcontainers/features/go:1.0.0",
		},
		{
			name:     "without namespace",
			registry: testPublishRegistry,
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
			registry:  testPublishRegistry,
			namespace: "test",
			id:        "python",
			version:   testPublishLatest,
			want:      "ghcr.io/test/python:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(
				t,
				tt.want,
				buildPublishReference(tt.registry, tt.namespace, tt.id, tt.version),
			)
		})
	}
}

func TestParsePublishReference_Valid(t *testing.T) {
	ref, err := ParsePublishReference(testPublishRegistry, "devcontainers/features", "go", "1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/devcontainers/features/go:1.0.0", ref.String())
}

func TestParsePublishReference_Invalid(t *testing.T) {
	_, err := ParsePublishReference("", "", "INVALID REF!!!", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse publish reference")
}

func TestValidatePublishDir_NotADirectory(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("hello"), 0o600))

	err := ValidatePublishDir(tmpFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target must be a directory")
}

func TestValidatePublishDir_NonexistentPath(t *testing.T) {
	err := ValidatePublishDir("/nonexistent/path/that/does/not/exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat target")
}

func TestBuildPublishImage_SingleLayer(t *testing.T) {
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

	img, err := BuildPublishImage("feature", tmpDir)
	require.NoError(t, err)
	require.NotNil(t, img)

	layers, err := img.Layers()
	require.NoError(t, err)
	assert.Len(t, layers, 1)
}

func TestBuildPublishImage_ErrorNamesArtifactKind(t *testing.T) {
	_, err := BuildPublishImage("template", "/nonexistent/path/that/does/not/exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create template archive")
}

func TestWritePublishedMetadata_IndentedWithTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WritePublishedMetadata(&buf, map[string]string{"id": "go"}))

	assert.Equal(t, "{\n  \"id\": \"go\"\n}\n", buf.String())
}
