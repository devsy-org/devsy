package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	pkgsnapshot "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
)

// buildTestTar writes a tiny single-file tar+gzip archive in memory,
// mirroring the shape produced by pkg/extract.WriteTarExclude.
func buildTestTar(t *testing.T, name, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	return buf.Bytes()
}

// testSnapshotParams configures pushTestSnapshot; fields only differ in
// mountPrefix/tarEntryName across the RestoreVolumes tests that share it.
type testSnapshotParams struct {
	repo         string
	tarEntryName string
	mountPrefix  string
}

// pushTestSnapshot pushes a single-file volumes tar plus a manifest
// referencing it (with mountPrefix, or omitted entirely when mountPrefix is
// ""), returning the resulting ref.
func pushTestSnapshot(t *testing.T, ctx context.Context, p testSnapshotParams) *pkgsnapshot.Ref {
	t.Helper()

	tarBytes := buildTestTar(t, p.tarEntryName, "hi")
	volDigest, volSize, err := pkgsnapshot.PushBlob(
		ctx,
		p.repo,
		pkgsnapshot.VolumesMediaType,
		strings.NewReader(string(tarBytes)),
	)
	require.NoError(t, err)
	imgDigest, imgSize, err := pkgsnapshot.PushBlob(
		ctx,
		p.repo,
		"application/vnd.docker.distribution.manifest.v2+json",
		strings.NewReader("img"),
	)
	require.NoError(t, err)

	m, err := pkgsnapshot.BuildManifest(pkgsnapshot.BuildManifestOptions{
		WorkspaceUID: "uid-1", CreatedAt: time.Now(), SourceProvider: "docker",
		MountPrefix:          p.mountPrefix,
		ContainerImageDigest: imgDigest, ContainerImageSize: imgSize,
		VolumesDigest: volDigest, VolumesSize: volSize,
	})
	require.NoError(t, err)
	ref, err := pkgsnapshot.NewRef(p.repo, "my-ws", time.Now())
	require.NoError(t, err)
	require.NoError(t, pkgsnapshot.PushManifest(ctx, ref.String(), m))
	return ref
}

func TestRestoreVolumes_ExtractsIntoMountTargets(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo := host + "/acme/snapshots"
	ctx := context.Background()

	ref := pushTestSnapshot(t, ctx, testSnapshotParams{
		repo: repo, tarEntryName: "workspaces/e2e/hello.txt", mountPrefix: "workspaces/e2e",
	})

	dest := t.TempDir()
	mounts := []*config.Mount{{Target: filepath.Join(dest, "workspaces/e2e")}}

	require.NoError(t, RestoreVolumes(ctx, ref.String(), mounts, false))

	gotPath := filepath.Join(dest, "workspaces/e2e/hello.txt")
	got, err := os.ReadFile(gotPath) //nolint:gosec // dest is t.TempDir()
	require.NoError(t, err)
	require.Equal(t, "hi", string(got))
}

func TestRestoreVolumes_ResetRemovesContentNotInSnapshot(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo := host + "/acme/snapshots"
	ctx := context.Background()

	ref := pushTestSnapshot(t, ctx, testSnapshotParams{
		repo: repo, tarEntryName: "workspaces/e2e/hello.txt", mountPrefix: "workspaces/e2e",
	})

	dest := t.TempDir()
	target := filepath.Join(dest, "workspaces/e2e")
	mounts := []*config.Mount{{Target: target}}

	require.NoError(t, RestoreVolumes(ctx, ref.String(), mounts, false))

	// Content added after the first restore, not part of the snapshot at all.
	stalePath := filepath.Join(target, "stale.txt")
	require.NoError(t, os.WriteFile(stalePath, []byte("stale"), 0o600))

	require.NoError(t, RestoreVolumes(ctx, ref.String(), mounts, true))

	_, err := os.Stat(stalePath)
	require.True(
		t, os.IsNotExist(err),
		"stale.txt should not survive a reset restore, got err=%v", err,
	)

	//nolint:gosec // dest is t.TempDir()
	got, err := os.ReadFile(filepath.Join(target, "hello.txt"))
	require.NoError(t, err)
	require.Equal(t, "hi", string(got))
}

// TestRestoreVolumes_UsesCreateTimePrefixNotRestoreTargetDepth guards against
// a regression where strip depth was derived from the restore-side mount
// target's own path-segment count instead of the recorded create-time
// prefix. Here the create-time prefix is 1 segment ("workspace") but the
// restore-side target is 2 segments deep ("workspaces/proj") — deriving
// depth from the restore side would strip 2 levels and destroy the
// filename; deriving it from the recorded prefix strips exactly 1 and
// restores correctly.
func TestRestoreVolumes_UsesCreateTimePrefixNotRestoreTargetDepth(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo := host + "/acme/snapshots"
	ctx := context.Background()

	ref := pushTestSnapshot(t, ctx, testSnapshotParams{
		repo: repo, tarEntryName: "workspace/hello.txt", mountPrefix: "workspace",
	})

	dest := t.TempDir()
	mounts := []*config.Mount{{Target: filepath.Join(dest, "workspaces/proj")}}

	require.NoError(t, RestoreVolumes(ctx, ref.String(), mounts, false))

	gotPath := filepath.Join(dest, "workspaces/proj/hello.txt")
	got, err := os.ReadFile(gotPath) //nolint:gosec // dest is t.TempDir()
	require.NoError(t, err)
	require.Equal(t, "hi", string(got))
}

// TestRestoreVolumes_ErrorsWhenMountPrefixAnnotationMissing guards against
// silently guessing a strip depth (and corrupting extracted paths) when a
// manifest lacks the annotation restore needs.
func TestRestoreVolumes_ErrorsWhenMountPrefixAnnotationMissing(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo := host + "/acme/snapshots"
	ctx := context.Background()

	ref := pushTestSnapshot(
		t,
		ctx,
		testSnapshotParams{repo: repo, tarEntryName: "workspace/hello.txt"},
	)

	dest := t.TempDir()
	mounts := []*config.Mount{{Target: filepath.Join(dest, "workspaces/proj")}}

	err := RestoreVolumes(ctx, ref.String(), mounts, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mount-prefix")
}
