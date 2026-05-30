package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateDocsCmd_Run(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "my-feature")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))

	featureJSON := `{
		"id": "my-feature",
		"version": "1.0.0",
		"name": "My Feature",
		"description": "A test feature",
		"documentationURL": "https://example.com/docs",
		"options": {
			"version": {
				"type": "string",
				"default": "latest",
				"description": "Version to install"
			}
		}
	}`
	require.NoError(t, os.WriteFile(
		filepath.Join(srcDir, "devcontainer-feature.json"),
		[]byte(featureJSON),
		0o600,
	))

	outputDir := t.TempDir()

	cmd := &GenerateDocsCmd{
		ProjectFolder: projectDir,
		OutputFolder:  outputDir,
		Namespace:     "ghcr.io/test/features",
		GlobalFlags:   &flags.GlobalFlags{ResultFormat: output.ModePlain},
	}

	err := cmd.Run()
	require.NoError(t, err)

	docPath := filepath.Join(outputDir, "my-feature.md")
	assert.FileExists(t, docPath)

	content, err := os.ReadFile(filepath.Clean(docPath))
	require.NoError(t, err)
	docContent := string(content)

	assert.Contains(t, docContent, "# My Feature")
	assert.Contains(t, docContent, "A test feature")
	assert.Contains(t, docContent, "`my-feature`")
	assert.Contains(t, docContent, "`1.0.0`")
	assert.Contains(t, docContent, "ghcr.io/test/features")
	assert.Contains(t, docContent, "## Options")
	assert.Contains(t, docContent, "`version`")
	assert.Contains(t, docContent, "`latest`")

	indexPath := filepath.Join(outputDir, "README.md")
	assert.FileExists(t, indexPath)

	indexContent, err := os.ReadFile(filepath.Clean(indexPath))
	require.NoError(t, err)
	assert.Contains(t, string(indexContent), "My Feature")
	assert.Contains(t, string(indexContent), "A test feature")
}

func TestGenerateDocsCmd_NoFeatures(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))

	cmd := &GenerateDocsCmd{
		ProjectFolder: projectDir,
		GlobalFlags:   &flags.GlobalFlags{ResultFormat: output.ModePlain},
	}

	err := cmd.Run()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no features found")
}

func TestGenerateDocsCmd_MultipleFeatures(t *testing.T) {
	projectDir := t.TempDir()

	for _, feat := range []struct {
		id   string
		name string
	}{
		{"go", "Go"},
		{"node", "Node.js"},
	} {
		srcDir := filepath.Join(projectDir, "src", feat.id)
		require.NoError(t, os.MkdirAll(srcDir, 0o750))
		featureJSON := `{"id": "` + feat.id + `", "name": "` + feat.name + `", "version": "1.0.0"}`
		require.NoError(t, os.WriteFile(
			filepath.Join(srcDir, "devcontainer-feature.json"),
			[]byte(featureJSON),
			0o600,
		))
	}

	outputDir := t.TempDir()
	cmd := &GenerateDocsCmd{
		ProjectFolder: projectDir,
		OutputFolder:  outputDir,
		GlobalFlags:   &flags.GlobalFlags{ResultFormat: output.ModePlain},
	}

	err := cmd.Run()
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(outputDir, "go.md"))
	assert.FileExists(t, filepath.Join(outputDir, "node.md"))
	assert.FileExists(t, filepath.Join(outputDir, "README.md"))
}
