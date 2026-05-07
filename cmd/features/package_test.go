package features

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageCmd_FlagDefaults(t *testing.T) {
	cmd := NewPackageCmd(nil)

	targetFlag := cmd.Flags().Lookup("target")
	require.NotNil(t, targetFlag)
	assert.Equal(t, "", targetFlag.DefValue)

	outputFolderFlag := cmd.Flags().Lookup("output-folder")
	require.NotNil(t, outputFolderFlag)
	assert.Equal(t, ".", outputFolderFlag.DefValue)

	forceCleanFlag := cmd.Flags().Lookup("force-clean-output-folder")
	require.NotNil(t, forceCleanFlag)
	assert.Equal(t, "false", forceCleanFlag.DefValue)

	outputFlag := cmd.Flags().Lookup("output")
	require.NotNil(t, outputFlag)
	assert.Equal(t, "text", outputFlag.DefValue)
}

func TestPackageCmd_AllFlagsRegistered(t *testing.T) {
	cmd := NewPackageCmd(nil)
	expected := []string{
		"target",
		"output-folder",
		"force-clean-output-folder",
		"output",
	}
	for _, name := range expected {
		assert.NotNil(t, cmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
}

func TestPackageCmd_DiscoverFeatures(t *testing.T) {
	targetDir := t.TempDir()

	featureADir := filepath.Join(targetDir, "feature-a")
	require.NoError(t, os.MkdirAll(featureADir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureADir, "devcontainer-feature.json"),
		[]byte(`{"id":"feature-a","version":"1.0.0","name":"Feature A"}`),
		0o600,
	))

	featureBDir := filepath.Join(targetDir, "feature-b")
	require.NoError(t, os.MkdirAll(featureBDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureBDir, "devcontainer-feature.json"),
		[]byte(`{"id":"feature-b","version":"2.0.0","name":"Feature B"}`),
		0o600,
	))

	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "not-a-dir.txt"),
		[]byte("ignored"),
		0o600,
	))

	cmd := &PackageCmd{}
	features, err := cmd.discoverFeatures(targetDir)
	require.NoError(t, err)
	assert.Len(t, features, 2)

	ids := make(map[string]bool)
	for _, f := range features {
		ids[f.config.ID] = true
	}
	assert.True(t, ids["feature-a"])
	assert.True(t, ids["feature-b"])
}

func TestPackageCmd_DiscoverFeatures_EmptyDir(t *testing.T) {
	targetDir := t.TempDir()

	cmd := &PackageCmd{}
	_, err := cmd.discoverFeatures(targetDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no features found")
}

func TestPackageCmd_DiscoverFeatures_NonexistentDir(t *testing.T) {
	cmd := &PackageCmd{}
	_, err := cmd.discoverFeatures("/nonexistent/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read target directory")
}

func TestPackageCmd_PackageFeature(t *testing.T) {
	targetDir := t.TempDir()
	outputDir := t.TempDir()

	featureDir := filepath.Join(targetDir, "my-feature")
	require.NoError(t, os.MkdirAll(featureDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureDir, "devcontainer-feature.json"),
		[]byte(`{"id":"my-feature","version":"1.2.3","name":"My Feature"}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureDir, "install.sh"),
		[]byte("#!/bin/bash\necho hello\n"),
		0o600,
	))

	cmd := &PackageCmd{}

	features, err := cmd.discoverFeatures(targetDir)
	require.NoError(t, err)
	require.Len(t, features, 1)

	result, err := cmd.packageFeature(features[0], targetDir, outputDir)
	require.NoError(t, err)

	assert.Equal(t, "my-feature", result.FeatureID)
	assert.Equal(t, "1.2.3", result.Version)
	assert.Equal(t, "devcontainer-feature-my-feature.tgz", result.Filename)

	archivePath := filepath.Join(outputDir, result.Filename)
	assert.FileExists(t, archivePath)

	files := readTarGzEntries(t, archivePath)
	assert.Contains(t, files, "devcontainer-feature.json")
	assert.Contains(t, files, "install.sh")
}

func TestPackageCmd_ForceCleanOutputFolder(t *testing.T) {
	targetDir := t.TempDir()
	outputDir := t.TempDir()

	featureDir := filepath.Join(targetDir, "feat")
	require.NoError(t, os.MkdirAll(featureDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureDir, "devcontainer-feature.json"),
		[]byte(`{"id":"feat","version":"1.0.0"}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureDir, "install.sh"),
		[]byte("#!/bin/bash\n"),
		0o600,
	))

	existingFile := filepath.Join(outputDir, "old-file.txt")
	require.NoError(t, os.WriteFile(existingFile, []byte("old"), 0o600))

	cmd := &PackageCmd{
		Target:                 targetDir,
		OutputFolder:           outputDir,
		ForceCleanOutputFolder: true,
		Output:                 outputText,
	}

	err := cmd.Run()
	require.NoError(t, err)

	_, statErr := os.Stat(existingFile)
	assert.True(t, os.IsNotExist(statErr), "old file should be removed")

	assert.FileExists(t, filepath.Join(outputDir, "devcontainer-feature-feat.tgz"))
}

func TestPackageCmd_InvalidOutputFormat(t *testing.T) {
	cmd := &PackageCmd{
		Target: "/tmp",
		Output: outputYAML,
	}
	err := cmd.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output format")
}

func TestPackageCmd_InvalidFeatureID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"path traversal", "../../../etc/passwd"},
		{"contains slash", "bad/id"},
		{"starts with hyphen", "-invalid"},
		{"uppercase letters", "MyFeature"},
		{"contains spaces", "bad id"},
		{"empty string", ""},
	}

	cmd := &PackageCmd{}
	targetDir := t.TempDir()
	outputDir := t.TempDir()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feat := featureSource{
				dir:    "some-dir",
				config: &config.FeatureConfig{ID: tt.id},
			}
			_, err := cmd.packageFeature(feat, targetDir, outputDir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid feature ID")
		})
	}
}

func TestPackageCmd_ValidFeatureIDs(t *testing.T) {
	tests := []string{"my-feature", "a", "feature-1", "0cool"}

	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			assert.True(t, validFeatureID.MatchString(id), "ID %q should be valid", id)
		})
	}
}

func readTarGzEntries(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path) // #nosec G304 -- test helper
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	var files []string
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		files = append(files, hdr.Name)
	}
	return files
}
