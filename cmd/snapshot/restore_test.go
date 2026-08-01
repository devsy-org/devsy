package snapshot

import (
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/provider"
	pkgsnapshot "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/stretchr/testify/require"
)

func TestBuildWorkspace_SetsSnapshotSourceAndImageOverride(t *testing.T) {
	cmd := &RestoreCmd{WorkspaceID: "restored-ws", ProviderName: testProviderName}
	manifest, err := pkgsnapshot.BuildManifest(pkgsnapshot.BuildManifestOptions{
		WorkspaceUID: testWorkspaceUID, CreatedAt: time.Now(), SourceProvider: testProviderName,
		ContainerImageDigest: "sha256:" + zeroDigestForTest, ContainerImageSize: 1,
		VolumesDigest: "sha256:" + oneDigestForTest, VolumesSize: 1,
	})
	require.NoError(t, err)

	ws, err := cmd.buildWorkspace(
		manifest,
		testSnapshotRef,
		testDevContainerSrc,
	)
	require.NoError(t, err)

	require.Equal(t, "restored-ws", ws.ID)
	require.Equal(t, testSnapshotRef, ws.Source.Snapshot)
	require.Equal(t, provider.WorkspaceSourceSnapshot, ws.Source.Type())
	require.Equal(t, testDevContainerSrc, ws.DevContainerSource)
	require.Equal(t, testProviderName, ws.Provider.Name)
}

func TestBuildWorkspace_DefaultsWorkspaceIDFromRefWhenFlagUnset(t *testing.T) {
	cmd := &RestoreCmd{ProviderName: testProviderName}
	manifest, err := pkgsnapshot.BuildManifest(pkgsnapshot.BuildManifestOptions{
		WorkspaceUID: testWorkspaceUID, CreatedAt: time.Now(), SourceProvider: testProviderName,
		ContainerImageDigest: "sha256:" + zeroDigestForTest, ContainerImageSize: 1,
		VolumesDigest: "sha256:" + oneDigestForTest, VolumesSize: 1,
	})
	require.NoError(t, err)

	ws, err := cmd.buildWorkspace(
		manifest,
		testSnapshotRef,
		testDevContainerSrc,
	)
	require.NoError(t, err)

	require.Equal(
		t,
		"my-ws",
		ws.ID,
		"default workspace id must come from the snapshot ref, not the manifest's hashed workspace-uid annotation",
	)
}

func TestBuildWorkspace_ErrorsOnMalformedRefWhenIDFlagUnset(t *testing.T) {
	cmd := &RestoreCmd{ProviderName: testProviderName}
	manifest, err := pkgsnapshot.BuildManifest(pkgsnapshot.BuildManifestOptions{
		WorkspaceUID: testWorkspaceUID, CreatedAt: time.Now(), SourceProvider: testProviderName,
		ContainerImageDigest: "sha256:" + zeroDigestForTest, ContainerImageSize: 1,
		VolumesDigest: "sha256:" + oneDigestForTest, VolumesSize: 1,
	})
	require.NoError(t, err)

	_, err = cmd.buildWorkspace(manifest, "not a valid ref!!", "image:whatever-fs")
	require.Error(t, err)
}

const (
	zeroDigestForTest   = "0000000000000000000000000000000000000000000000000000000000000000"
	oneDigestForTest    = "1111111111111111111111111111111111111111111111111111111111111111"
	testProviderName    = "docker"
	testWorkspaceUID    = "uid-1"
	testSnapshotRef     = "ghcr.io/acme/s:my-ws-20260731150405-abcxyz"
	testDevContainerSrc = "image:ghcr.io/acme/s:my-ws-20260731150405-abcxyz-fs"
)
