package devcontainer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/stretchr/testify/require"
)

func TestDockerlessGeneratedArtifactManifestHasOnlyGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	context := filepath.Join(root, ".devcontainer")
	internal := filepath.Join(context, config.DevsyContextFeatureFolder)
	require.NoError(t, os.MkdirAll(internal, 0o700))
	unrelated := filepath.Join(internal, "private.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("private"), 0o600))
	result, err := dockerlessFallback(
		&dockerlessFallbackParams{
			localWorkspaceFolder:     root,
			containerWorkspaceFolder: testWorkspaceFolder,
			parsedConfig: &config.SubstitutedConfig{
				Config: &config.DevContainerConfig{
					Origin: filepath.Join(context, "devcontainer.json"),
				},
			},
			extendedBuildInfo: &feature.ExtendedBuildInfo{},
			buildInfo:         &config.ImageBuildInfo{},
			dockerfileContent: "FROM scratch",
		},
	)
	require.NoError(t, err)
	require.Len(t, result.GeneratedBuildArtifacts, 1)
	require.Equal(
		t,
		filepath.Join(internal, "Dockerfile-without-features"),
		result.GeneratedBuildArtifacts[0].Path,
	)
	require.NotEmpty(t, result.GeneratedBuildArtifacts[0].SHA256)
}

func TestDockerlessArtifactSymlinkRejectedBeforeWrite(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("symlink creation requires privileges")
	}
	for _, linkFolder := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "folder"}[linkFolder], func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			internal := filepath.Join(root, config.DevsyContextFeatureFolder)
			sentinel := filepath.Join(outside, "Dockerfile-without-features")
			require.NoError(t, os.WriteFile(sentinel, []byte("unaltered"), 0o600))
			if linkFolder {
				require.NoError(t, os.Symlink(outside, internal))
			} else {
				require.NoError(t, os.Mkdir(internal, 0o700))
				require.NoError(
					t,
					os.Symlink(sentinel, filepath.Join(internal, "Dockerfile-without-features")),
				)
			}
			_, err := dockerlessFallback(
				&dockerlessFallbackParams{
					localWorkspaceFolder: root,
					parsedConfig: &config.SubstitutedConfig{
						Config: &config.DevContainerConfig{
							Origin: filepath.Join(root, "devcontainer.json"),
						},
					},
					extendedBuildInfo: &feature.ExtendedBuildInfo{},
					buildInfo:         &config.ImageBuildInfo{},
					dockerfileContent: "FROM scratch",
				},
			)
			require.ErrorContains(t, err, "symlink")
			// #nosec G304 -- sentinel is a file in the temporary test directory.
			contents, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			require.Equal(t, "unaltered", string(contents))
		})
	}
}
