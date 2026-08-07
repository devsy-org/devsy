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

	decompressed, err := compress.Decompress(compressed)
	require.NoError(t, err)

	var roundTripped config.Result
	require.NoError(t, json.Unmarshal([]byte(decompressed), &roundTripped))

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
