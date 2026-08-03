package snapshot

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestRestoreComposition_ComposesSourceAndDevContainerSource(t *testing.T) {
	source, devContainerSource, err := RestoreComposition(
		"ghcr.io/acme/s:my-ws-20260731150405-abcxyz",
	)
	require.NoError(t, err)
	require.Equal(
		t,
		provider.WorkspaceSourceSnapshot+"ghcr.io/acme/s:my-ws-20260731150405-abcxyz",
		source,
	)
	require.Equal(t, "image:ghcr.io/acme/s:my-ws-20260731150405-abcxyz-fs", devContainerSource)
}

func TestRestoreComposition_NormalizesShortRefToMatchDevContainerSource(t *testing.T) {
	source, devContainerSource, err := RestoreComposition("acme/s:my-ws-20260731150405-abcxyz")
	require.NoError(t, err)
	require.Equal(
		t,
		provider.WorkspaceSourceSnapshot+"index.docker.io/acme/s:my-ws-20260731150405-abcxyz",
		source,
	)
	require.Equal(
		t,
		"image:index.docker.io/acme/s:my-ws-20260731150405-abcxyz-fs",
		devContainerSource,
	)
}

func TestRestoreComposition_ErrorsOnBadRef(t *testing.T) {
	_, _, err := RestoreComposition("not a valid ref!!")
	require.Error(t, err)
}

func TestRef_FSImageRef(t *testing.T) {
	ref, err := ParseRef("ghcr.io/acme/snapshots:my-ws-20260731150405-abcxyz")
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/acme/snapshots:my-ws-20260731150405-abcxyz-fs", ref.FSImageRef())
}
