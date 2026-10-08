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

func TestGeneratedFeatureSymlinksRecordTargetsWithoutDereferencing(t *testing.T) {
	for _, targetKind := range []string{"relative", "dangling", "external-directory"} {
		t.Run(targetKind, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, config.DevsyContextFeatureFolder)
			require.NoError(t, os.MkdirAll(folder, 0o700))
			dockerfile := filepath.Join(folder, "Dockerfile-with-features")
			require.NoError(t, os.WriteFile(dockerfile, []byte("FROM scratch"), 0o600))
			target := "missing-relative-target"
			if targetKind == "relative" {
				target = "../regular-target"
				require.NoError(
					t,
					os.WriteFile(
						filepath.Join(root, "regular-target"),
						[]byte("outside generated context"),
						0o600,
					),
				)
			}
			if targetKind == "external-directory" {
				target = t.TempDir()
				require.NoError(
					t,
					os.WriteFile(
						filepath.Join(target, "secret.txt"),
						[]byte("outside workspace"),
						0o600,
					),
				)
			}
			link := filepath.Join(folder, "generated-link")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			manifest, err := dockerlessArtifactManifest(
				dockerfile,
				&feature.ExtendedBuildInfo{
					FeaturesBuildInfo: &feature.BuildInfo{FeaturesFolder: folder},
				},
			)
			require.NoError(t, err)
			require.Len(t, manifest, 2)
			require.Equal(
				t,
				config.GeneratedBuildArtifact{Path: link, LinkTarget: target},
				manifest[1],
			)
			require.NotEmpty(t, manifest[0].SHA256)
		})
	}
}

func TestGeneratedManifestIncludesOnlyEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, config.DevsyContextFeatureFolder)
	empty := filepath.Join(folder, "feature", "empty")
	require.NoError(t, os.MkdirAll(empty, 0o700))
	dockerfile := filepath.Join(folder, "Dockerfile-with-features")
	require.NoError(t, os.WriteFile(dockerfile, []byte("FROM scratch"), 0o600))
	manifest, err := dockerlessArtifactManifest(
		dockerfile,
		&feature.ExtendedBuildInfo{FeaturesBuildInfo: &feature.BuildInfo{FeaturesFolder: folder}},
	)
	require.NoError(t, err)
	require.Len(t, manifest, 2)
	require.Equal(t, config.GeneratedBuildArtifact{Path: empty, Directory: true}, manifest[1])
	require.NotEmpty(t, manifest[0].SHA256)
}

func TestGeneratedDirectorySymlinkDoesNotRecordTargetDirectories(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, config.DevsyContextFeatureFolder)
	require.NoError(t, os.MkdirAll(folder, 0o700))
	dockerfile := filepath.Join(folder, "Dockerfile-with-features")
	require.NoError(t, os.WriteFile(dockerfile, []byte("FROM scratch"), 0o600))
	external := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(external, "unread-directory"), 0o700))
	link := filepath.Join(folder, "generated-link")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	manifest, err := dockerlessArtifactManifest(
		dockerfile,
		&feature.ExtendedBuildInfo{FeaturesBuildInfo: &feature.BuildInfo{FeaturesFolder: folder}},
	)
	require.NoError(t, err)
	require.Len(t, manifest, 2)
	require.Equal(t, config.GeneratedBuildArtifact{Path: link, LinkTarget: external}, manifest[1])
}
