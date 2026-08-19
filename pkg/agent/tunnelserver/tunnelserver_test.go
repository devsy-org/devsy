package tunnelserver

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

type fakeStreamServer struct {
	tunnel.Tunnel_StreamWorkspaceServer
	chunks [][]byte
}

func (f *fakeStreamServer) Send(c *tunnel.Chunk) error {
	f.chunks = append(f.chunks, c.Content)
	return nil
}

func (f *fakeStreamServer) Context() context.Context { return context.Background() }

func tarEntryNames(t *testing.T, chunks [][]byte) []string {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(c)
	}
	var names []string
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
	return names
}

func TestStreamWorkspace_IncludesDevsyInternalBuildArtifacts(t *testing.T) {
	dir := t.TempDir()
	internalDir := filepath.Join(dir, config.DevsyContextFeatureFolder)
	require.NoError(t, os.MkdirAll(internalDir, 0o750))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(internalDir, "Dockerfile-without-features"),
			[]byte("FROM scratch"),
			0o600,
		),
	)

	srv := &tunnelServer{
		workspace: &provider2.Workspace{Source: provider2.WorkspaceSource{LocalFolder: dir}},
	}

	fake := &fakeStreamServer{}
	require.NoError(t, srv.StreamWorkspace(&tunnel.Empty{}, fake))

	names := tarEntryNames(t, fake.chunks)
	require.Contains(
		t,
		names,
		filepath.ToSlash(filepath.Join(
			config.DevsyContextFeatureFolder,
			"Dockerfile-without-features",
		)),
	)
}

func TestStreamMount_IncludesDevsyInternalBuildArtifacts(t *testing.T) {
	dir := t.TempDir()
	mountSource := filepath.Join(dir, "mount")
	internalDir := filepath.Join(mountSource, config.DevsyContextFeatureFolder)
	require.NoError(t, os.MkdirAll(internalDir, 0o750))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(internalDir, "Dockerfile-without-features"),
			[]byte("FROM scratch"),
			0o600,
		),
	)

	srv := &tunnelServer{
		mounts: []*config.Mount{
			{Source: mountSource, Target: "/workspaces/e2e"},
		},
	}

	fake := &fakeStreamServer{}
	require.NoError(
		t,
		srv.StreamMount(&tunnel.StreamMountRequest{Mount: srv.mounts[0].String()}, fake),
	)

	names := tarEntryNames(t, fake.chunks)
	require.Contains(
		t,
		names,
		filepath.ToSlash(filepath.Join(
			config.DevsyContextFeatureFolder,
			"Dockerfile-without-features",
		)),
	)
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
	require.ErrorIs(t, err, context.Canceled)
}

func TestRun_CancelBeforeResultIsExpected(t *testing.T) {
	srv := New()

	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, srv.Run(ctx, reader, writer))
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

func TestRunWithResult_ConcurrentSendResult(t *testing.T) {
	srv := New()

	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	sendDone := make(chan error, 1)
	go func() {
		_, err := srv.SendResult(context.Background(), &tunnel.Message{Message: "{}"})
		sendDone <- err
		cancel()
	}()

	result, err := srv.RunWithResult(ctx, reader, writer)

	require.NoError(t, <-sendDone)
	require.NoError(t, err)
	require.NotNil(t, result)
}
