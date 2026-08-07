package tunnelserver

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

// fakeStreamSnapshotVolumesServer captures Send calls so
// StreamSnapshotVolumes can be exercised without a real gRPC connection.
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

func TestRunWithResult_CancelBeforeResult(t *testing.T) {
	srv := New()

	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, err := srv.RunWithResult(ctx, reader, writer)
	require.Nil(t, result)
	require.True(t, errors.Is(err, context.Canceled), "got: %v", err)
}

func TestRunWithResult_CancelAfterResult(t *testing.T) {
	srv := New()
	want := &config.Result{}
	srv.setResult(want)

	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, err := srv.RunWithResult(ctx, reader, writer)
	require.NoError(t, err)
	require.Same(t, want, result)
}

// TestRunWithResult_ConcurrentSendResult exercises SendResult's write racing
// against RunWithResult's read under -race, guarding the getResult/setResult
// mutex added to fix that unsynchronized access.
func TestRunWithResult_ConcurrentSendResult(t *testing.T) {
	srv := New()

	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, _ = srv.SendResult(context.Background(), &tunnel.Message{Message: "{}"})
		cancel()
	}()

	_, _ = srv.RunWithResult(ctx, reader, writer)
}
