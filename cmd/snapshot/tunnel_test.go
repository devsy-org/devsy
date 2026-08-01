package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o600))

	mounts := []*devcontainerconfig.Mount{
		{Type: testBindMountType, Source: dir, Target: "/workspaces/proj"},
	}

	client, cleanup, err := newLocalTunnelClient(context.Background(), mounts)
	require.NoError(t, err)
	defer cleanup()

	stream, err := client.StreamSnapshotVolumes(context.Background(), &tunnel.Empty{})
	require.NoError(t, err)

	var total int
	for {
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			break
		}
		total += len(chunk.Content)
	}
	require.Greater(t, total, 0)
}
