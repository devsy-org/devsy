package snapshot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildManifest_RoundTrip(t *testing.T) {
	opts := BuildManifestOptions{
		WorkspaceUID:         testWorkspaceUID,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Parent:               "",
		DevContainerHash:     "abc123",
		SourceProvider:       testSourceProvider,
		ContainerImageDigest: "sha256:" + zeroDigest,
		ContainerImageSize:   1024,
		VolumesDigest:        "sha256:" + oneDigest,
		VolumesSize:          2048,
	}

	built, err := BuildManifest(opts)
	require.NoError(t, err)

	raw, err := built.MarshalOCI()
	require.NoError(t, err)

	parsed, err := ParseManifest(raw)
	require.NoError(t, err)

	require.Equal(t, opts.WorkspaceUID, parsed.Annotations[AnnotationWorkspaceUID])
	require.Equal(t, "2026-07-31T12:00:00Z", parsed.Annotations[AnnotationCreatedAt])
	require.Equal(t, "", parsed.Annotations[AnnotationParent])
	require.Equal(t, opts.DevContainerHash, parsed.Annotations[AnnotationDevContainerHash])
	require.Equal(t, opts.SourceProvider, parsed.Annotations[AnnotationSourceProvider])
	require.Equal(t, ManifestArtifactType, parsed.ArtifactType)
	require.Equal(t, 2, parsed.SchemaVersion)
	require.Len(t, parsed.Layers, 2)
	require.Equal(t, opts.ContainerImageDigest, parsed.Layers[0].Digest)
	require.Equal(t, VolumesMediaType, parsed.Layers[1].MediaType)
	require.Equal(t, opts.VolumesDigest, parsed.Layers[1].Digest)
}

func TestBuildManifest_MessageAnnotationOnlyWhenNonEmpty(t *testing.T) {
	base := BuildManifestOptions{
		WorkspaceUID:         testWorkspaceUID,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		ContainerImageDigest: "sha256:" + zeroDigest,
		VolumesDigest:        "sha256:" + oneDigest,
	}

	withMessage := base
	withMessage.Message = "before the big refactor"

	built, err := BuildManifest(withMessage)
	require.NoError(t, err)
	raw, err := built.MarshalOCI()
	require.NoError(t, err)
	parsed, err := ParseManifest(raw)
	require.NoError(t, err)
	require.Equal(t, "before the big refactor", parsed.Annotations[AnnotationMessage])

	built, err = BuildManifest(base)
	require.NoError(t, err)
	raw, err = built.MarshalOCI()
	require.NoError(t, err)
	parsed, err = ParseManifest(raw)
	require.NoError(t, err)
	_, ok := parsed.Annotations[AnnotationMessage]
	require.False(t, ok)
}

func TestBuildManifest_RunArgsRoundTrip(t *testing.T) {
	opts := BuildManifestOptions{
		WorkspaceUID:         testWorkspaceUID,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		ContainerImageDigest: "sha256:" + zeroDigest,
		VolumesDigest:        "sha256:" + oneDigest,
		RunArgs:              []string{"--add-host=host.docker.internal:host-gateway"},
	}

	built, err := BuildManifest(opts)
	require.NoError(t, err)

	raw, err := built.MarshalOCI()
	require.NoError(t, err)
	parsed, err := ParseManifest(raw)
	require.NoError(t, err)

	runArgs, err := parsed.RunArgs()
	require.NoError(t, err)
	require.Equal(t, opts.RunArgs, runArgs)
}

func TestManifest_RunArgs_NilWhenAbsent(t *testing.T) {
	opts := BuildManifestOptions{
		WorkspaceUID:         testWorkspaceUID,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		ContainerImageDigest: "sha256:" + zeroDigest,
		VolumesDigest:        "sha256:" + oneDigest,
	}

	built, err := BuildManifest(opts)
	require.NoError(t, err)
	runArgs, err := built.RunArgs()
	require.NoError(t, err)
	require.Nil(t, runArgs)
}

func TestBuildManifest_ContainerEnvRoundTrip(t *testing.T) {
	opts := BuildManifestOptions{
		WorkspaceUID:         testWorkspaceUID,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		ContainerImageDigest: "sha256:" + zeroDigest,
		VolumesDigest:        "sha256:" + oneDigest,
		ContainerEnv:         map[string]string{"DEVSY_INSECURE_DOCKER_INTERNAL": "true"},
	}

	built, err := BuildManifest(opts)
	require.NoError(t, err)

	raw, err := built.MarshalOCI()
	require.NoError(t, err)
	parsed, err := ParseManifest(raw)
	require.NoError(t, err)

	containerEnv, err := parsed.ContainerEnv()
	require.NoError(t, err)
	require.Equal(t, opts.ContainerEnv, containerEnv)
}

func TestManifest_ContainerEnv_NilWhenAbsent(t *testing.T) {
	opts := BuildManifestOptions{
		WorkspaceUID:         testWorkspaceUID,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		ContainerImageDigest: "sha256:" + zeroDigest,
		VolumesDigest:        "sha256:" + oneDigest,
	}

	built, err := BuildManifest(opts)
	require.NoError(t, err)
	containerEnv, err := built.ContainerEnv()
	require.NoError(t, err)
	require.Nil(t, containerEnv)
}

func TestParseManifest_RejectsInvalidSchemaVersion(t *testing.T) {
	invalidManifest := []byte(
		`{"schemaVersion":1,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`,
	)
	_, err := ParseManifest(invalidManifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported schemaVersion")
}

func TestParseManifest_RejectsNonSnapshotArtifactType(t *testing.T) {
	// A plain OCI/Docker image manifest also has schemaVersion 2, so it must
	// be rejected by artifactType instead, or a tag pointing at an ordinary
	// image would be silently accepted as a snapshot.
	imageManifest := []byte(
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`,
	)
	_, err := ParseManifest(imageManifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported artifactType")
}

var (
	zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"[:64]
	oneDigest  = "1111111111111111111111111111111111111111111111111111111111111111"[:64]
)

const (
	testSourceProvider = "docker"
	testWorkspaceUID   = "uid-123"
)
