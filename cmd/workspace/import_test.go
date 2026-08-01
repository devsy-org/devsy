package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportWorkspace_SnapshotRefRestoresSourceAndDevContainerSource(t *testing.T) {
	srcStr, devContainerSource, err := snapshotpkg.RestoreComposition(
		"ghcr.io/acme/s:my-ws-20260731150405-abcxyz",
	)
	require.NoError(t, err)

	parsedSource := provider.ParseWorkspaceSource(srcStr)
	require.NotNil(t, parsedSource)

	assert.Equal(t, provider.WorkspaceSourceSnapshot, parsedSource.Type())
	assert.Equal(t, "ghcr.io/acme/s:my-ws-20260731150405-abcxyz", parsedSource.Snapshot)
	assert.Equal(t, "image:ghcr.io/acme/s:my-ws-20260731150405-abcxyz-fs", devContainerSource)
}

func TestImportWorkspace_SnapshotRefBadRef(t *testing.T) {
	_, _, err := snapshotpkg.RestoreComposition("invalid-ref")
	require.Error(t, err)
}
