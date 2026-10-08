package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

// TestNewLocalTunnelClient_StreamsMountContents exercises the in-process
// tunnel wiring end-to-end: a tunnelServer serving StreamSnapshotVolumes off
// a real mount directory, dialed by a tunnel.TunnelClient over an in-process
// pipe pair (no SSH hop), mirroring how PushVolumesFromTunnel is invoked in
// Run.
func TestNewLocalTunnelClient_StreamsMountContents(t *testing.T) {
	dir := snapshotIgnoreFixture(t)

	mounts := []*devcontainerconfig.Mount{
		{Type: testBindMountType, Source: dir, Target: "/workspaces/proj"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, cleanup, err := newLocalTunnelClient(ctx, mounts, mounts[0])
	require.NoError(t, err)
	defer cleanup()

	stream, err := client.StreamSnapshotVolumes(ctx, &tunnel.Empty{})
	require.NoError(t, err)

	var archive bytes.Buffer
	for {
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			require.ErrorIs(t, recvErr, io.EOF)
			break
		}
		_, err = archive.Write(chunk.Content)
		require.NoError(t, err)
	}
	reader := tar.NewReader(&archive)
	var names []string
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		names = append(names, header.Name)
	}
	require.Contains(t, names, "workspaces/proj/hello.txt")
	require.NotContains(t, names, "workspaces/proj/omit.txt")
	require.Contains(
		t,
		names,
		"workspaces/proj/"+devcontainerconfig.DevsyContextFeatureFolder+"/retained.txt",
	)
}

func snapshotIgnoreFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "omit.txt"), []byte("omitted"), 0o600))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, ".devsyignore"), []byte("omit.txt\n"), 0o600),
	)
	require.NoError(
		t,
		os.Mkdir(filepath.Join(dir, devcontainerconfig.DevsyContextFeatureFolder), 0o700),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, devcontainerconfig.DevsyContextFeatureFolder, "retained.txt"),
			[]byte("user-owned"),
			0o600,
		),
	)

	return dir
}
