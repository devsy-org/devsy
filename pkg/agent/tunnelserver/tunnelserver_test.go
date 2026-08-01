package tunnelserver

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

// fakeStreamSnapshotVolumesServer captures Send calls without a real gRPC
// connection, mirroring how existing StreamMount/StreamWorkspace tests (if
// any) exercise server-streaming handlers directly.
type fakeStreamSnapshotVolumesServer struct {
	tunnel.Tunnel_StreamSnapshotVolumesServer
	chunks [][]byte
}

func (f *fakeStreamSnapshotVolumesServer) Send(c *tunnel.Chunk) error {
	f.chunks = append(f.chunks, c.Content)
	return nil
}

func (f *fakeStreamSnapshotVolumesServer) Context() context.Context { return context.Background() }

func TestStreamSnapshotVolumes_TarsMountTargets(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "workspace"), 0o750))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "workspace", "file.txt"), []byte("hi"), 0o600),
	)

	srv := &tunnelServer{
		workspace: &provider2.Workspace{Source: provider2.WorkspaceSource{LocalFolder: dir}},
		mounts: []*config.Mount{
			{Source: filepath.Join(dir, "workspace"), Target: "/workspaces/e2e"},
		},
	}

	fake := &fakeStreamSnapshotVolumesServer{}
	err := srv.StreamSnapshotVolumes(&tunnel.Empty{}, fake)
	require.NoError(t, err)
	require.NotEmpty(t, fake.chunks)

	var buf bytes.Buffer
	for _, c := range fake.chunks {
		buf.Write(c)
	}

	wantName := "workspaces/e2e/file.txt"
	tr := tar.NewReader(&buf)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Name == wantName {
			found = true
			content, err := io.ReadAll(tr)
			require.NoError(t, err)
			require.Equal(t, "hi", string(content))
		}
	}
	require.True(t, found, "expected tar entry %q not found", wantName)
}
