package feature

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAnnotations(t *testing.T) {
	dir := t.TempDir()
	annotations := map[string]string{
		"org.opencontainers.image.title":       "Go",
		"org.opencontainers.image.description": "Installs Go and common Go tools",
		"org.opencontainers.image.version":     "1.2.3",
		"org.opencontainers.image.source":      "https://github.com/devcontainers/features",
	}

	saveAnnotations(dir, annotations)

	data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, annotationsFileName)))
	require.NoError(t, err)

	var loaded map[string]string
	require.NoError(t, json.Unmarshal(data, &loaded))
	assert.Equal(t, annotations, loaded)
}

func TestSaveAnnotations_EmptyMap(t *testing.T) {
	dir := t.TempDir()
	saveAnnotations(dir, map[string]string{})

	data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, annotationsFileName)))
	require.NoError(t, err)

	var loaded map[string]string
	require.NoError(t, json.Unmarshal(data, &loaded))
	assert.Empty(t, loaded)
}

func TestLoadOCIAnnotations_Present(t *testing.T) {
	dir := t.TempDir()
	extractedDir := filepath.Join(dir, "extracted")
	require.NoError(t, os.MkdirAll(extractedDir, 0o750))

	annotations := map[string]string{
		"org.opencontainers.image.title":         "Node.js",
		"org.opencontainers.image.description":   "Installs Node.js and common npm tools",
		"org.opencontainers.image.authors":       "Dev Containers",
		"org.opencontainers.image.url":           "https://github.com/devcontainers/features/tree/main/src/node",
		"org.opencontainers.image.documentation": "https://containers.dev/features",
		"org.opencontainers.image.licenses":      "MIT",
		"dev.containers.metadata":                `{"id":"node"}`,
	}
	saveAnnotations(dir, annotations)

	loaded := LoadOCIAnnotations(extractedDir)
	assert.Equal(t, annotations, loaded)
}

func TestLoadOCIAnnotations_Missing(t *testing.T) {
	dir := t.TempDir()
	extractedDir := filepath.Join(dir, "extracted")
	require.NoError(t, os.MkdirAll(extractedDir, 0o750))

	loaded := LoadOCIAnnotations(extractedDir)
	assert.Nil(t, loaded)
}

func TestLoadOCIAnnotations_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	extractedDir := filepath.Join(dir, "extracted")
	require.NoError(t, os.MkdirAll(extractedDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, annotationsFileName),
		[]byte("not valid json"),
		0o600,
	))

	loaded := LoadOCIAnnotations(extractedDir)
	assert.Nil(t, loaded)
}

func TestLogOCIAnnotations_NoTitle(t *testing.T) {
	annotations := map[string]string{
		"org.opencontainers.image.source": "https://github.com/example/features",
	}
	// Should not panic with missing title/description
	logOCIAnnotations("ghcr.io/example/feature:1", annotations)
}

func TestLogOCIAnnotations_WithTitle(t *testing.T) {
	annotations := map[string]string{
		"org.opencontainers.image.title":       "Go",
		"org.opencontainers.image.description": "Installs Go",
		"org.opencontainers.image.version":     "1.0.0",
	}
	// Should not panic
	logOCIAnnotations("ghcr.io/devcontainers/features/go:1", annotations)
}
