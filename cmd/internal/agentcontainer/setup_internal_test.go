//go:build !windows

package agentcontainer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	pkgsnapshot "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressSetupInfoPreservesSubstitutedValues(t *testing.T) {
	// Simulate post-substitution state: PATH is a real value, not a
	// ${containerEnv:PATH} literal.
	info := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteEnv: map[string]*string{
					"PATH": new("/usr/local/bin:/usr/bin:/bin"),
					"HOME": new("/home/testuser"),
				},
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	compressed, err := compressSetupInfo(info)
	require.NoError(t, err)
	require.NotEmpty(t, compressed)

	// Round-trip: decompress and unmarshal.
	decompressed, err := compress.Decompress(compressed)
	require.NoError(t, err)

	var roundTripped config.Result
	require.NoError(t, json.Unmarshal([]byte(decompressed), &roundTripped))

	// The resolved PATH must come through, not a literal variable reference.
	gotPath := roundTripped.MergedConfig.RemoteEnv["PATH"]
	require.NotNil(t, gotPath)
	assert.Equal(t, "/usr/local/bin:/usr/bin:/bin", *gotPath)
	assert.False(t, strings.Contains(*gotPath, "${containerEnv:"),
		"PATH should be resolved, not contain ${containerEnv:} literals")
	gotHome := roundTripped.MergedConfig.RemoteEnv["HOME"]
	require.NotNil(t, gotHome)
	assert.Equal(t, "/home/testuser", *gotHome)
}

func TestSecretsEnvRoundTripPreservesMultilineValues(t *testing.T) {
	// PEM keys and certs carry newlines; the DEVSY_SECRETS_ENV round-trip must
	// preserve them exactly rather than truncating at the first newline.
	entries := []string{
		"TLS_KEY=-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----",
		"SIMPLE=value",
		"WITH_EQUALS=a=b=c",
	}

	encoded := encodeSecretsEnv(entries)
	t.Setenv("DEVSY_SECRETS_ENV", encoded)

	got := secretsEnvFromEnvironment()
	assert.Equal(t, entries, got)
}

// buildSnapshotTestTar writes a tiny single-file tar+gzip archive in memory,
// mirroring the shape produced by pkg/extract.WriteTarExclude.
func buildSnapshotTestTar(t *testing.T, name, content string) []byte {
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

// TestSyncMounts_SnapshotRestoresEvenWithStreamMountsFalse guards against a
// regression where the snapshot-restore branch lived behind the
// `!cmd.StreamMounts` early return. StreamMounts is only forced true for
// non-docker drivers, so on the default docker driver a snapshot-sourced
// workspace would silently skip restoring its volumes. This test drives the
// real syncMounts entry point (not RestoreVolumes directly) with
// StreamMounts=false to prove the restore still runs.
func TestSyncMounts_SnapshotRestoresEvenWithStreamMountsFalse(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo := host + "/acme/snapshots"
	ctx := context.Background()

	tarBytes := buildSnapshotTestTar(t, "workspaces/e2e/hello.txt", "hi")
	volDigest, volSize, err := pkgsnapshot.PushBlob(
		ctx,
		repo,
		pkgsnapshot.VolumesMediaType,
		strings.NewReader(string(tarBytes)),
	)
	require.NoError(t, err)
	imgDigest, imgSize, err := pkgsnapshot.PushBlob(
		ctx,
		repo,
		"application/vnd.docker.distribution.manifest.v2+json",
		strings.NewReader("img"),
	)
	require.NoError(t, err)

	manifest, err := pkgsnapshot.BuildManifest(pkgsnapshot.BuildManifestOptions{
		WorkspaceUID: "uid-1", CreatedAt: time.Now(), SourceProvider: "docker",
		MountPrefix:          "workspaces/e2e",
		ContainerImageDigest: imgDigest, ContainerImageSize: imgSize,
		VolumesDigest: volDigest, VolumesSize: volSize,
	})
	require.NoError(t, err)
	ref, err := pkgsnapshot.NewRef(repo, "my-ws", time.Now())
	require.NoError(t, err)
	require.NoError(t, pkgsnapshot.PushManifest(ctx, ref.String(), manifest))

	dest := t.TempDir()
	mountTarget := filepath.Join(dest, "workspaces/e2e")

	setupInfo := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount: "type=bind,target=" + mountTarget,
		},
		MergedConfig: &config.MergedDevContainerConfig{},
	}

	cmd := &SetupContainerCmd{StreamMounts: false}
	state := &containerState{
		setupInfo: setupInfo,
		workspaceInfo: &provider2.ContainerWorkspaceInfo{
			Source: provider2.WorkspaceSource{Snapshot: ref.String()},
		},
	}

	require.NoError(t, cmd.syncMounts(ctx, state))

	gotPath := filepath.Join(mountTarget, "hello.txt")
	got, err := os.ReadFile(gotPath) //nolint:gosec // mountTarget is t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "hi", string(got))
}

// TestSyncMounts_SnapshotSkipsRestoreWhenMountNotEmpty guards against
// re-extracting a snapshot's volumes over an already-populated mount (e.g. on
// container restart), which would silently discard local changes made since
// the last restore.
func TestSyncMounts_SnapshotSkipsRestoreWhenMountNotEmpty(t *testing.T) {
	ref, err := pkgsnapshot.NewRef("example.com/acme/snapshots", "my-ws", time.Now())
	require.NoError(t, err)

	dest := t.TempDir()
	mountTarget := filepath.Join(dest, "workspaces/e2e")
	require.NoError(t, os.MkdirAll(mountTarget, 0o750))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(mountTarget, "local-change.txt"), []byte("keep me"), 0o600),
	)

	setupInfo := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount: "type=bind,target=" + mountTarget,
		},
		MergedConfig: &config.MergedDevContainerConfig{},
	}

	cmd := &SetupContainerCmd{StreamMounts: false}
	state := &containerState{
		setupInfo: setupInfo,
		workspaceInfo: &provider2.ContainerWorkspaceInfo{
			Source: provider2.WorkspaceSource{Snapshot: ref.String()},
		},
	}

	require.NoError(t, cmd.syncMounts(context.Background(), state))

	gotPath := filepath.Join(mountTarget, "local-change.txt")
	got, err := os.ReadFile(gotPath) //nolint:gosec // mountTarget is t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(got))
}

// TestSyncMounts_SnapshotMultiMountFailsLoudly asserts that a snapshot
// source with more than one mount fails explicitly instead of silently
// restoring only the first mount and dropping the rest.
func TestSyncMounts_SnapshotMultiMountFailsLoudly(t *testing.T) {
	dest := t.TempDir()
	setupInfo := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount: "type=bind,target=" + filepath.Join(dest, "workspaces/e2e"),
		},
		MergedConfig: &config.MergedDevContainerConfig{
			NonComposeBase: config.NonComposeBase{
				Mounts: []*config.Mount{
					{Type: "bind", Source: "/host/extra", Target: filepath.Join(dest, "extra")},
				},
			},
		},
	}

	cmd := &SetupContainerCmd{StreamMounts: false}
	state := &containerState{
		setupInfo: setupInfo,
		workspaceInfo: &provider2.ContainerWorkspaceInfo{
			Source: provider2.WorkspaceSource{
				Snapshot: "example.com/acme/snapshots:my-ws-20260101000000",
			},
		},
	}

	err := cmd.syncMounts(context.Background(), state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not yet support multiple mounts")
}
