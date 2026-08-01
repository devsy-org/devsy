package snapshot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// fakeTunnelClient stubs only StreamSnapshotVolumes; every other method
// panics if called, since this test only exercises the volumes push path.
type fakeTunnelClient struct {
	tunnel.TunnelClient
	payload string
}

type fakeStreamSnapshotVolumesClient struct {
	tunnel.Tunnel_StreamSnapshotVolumesClient
	reader *strings.Reader
}

func (f *fakeStreamSnapshotVolumesClient) Recv() (*tunnel.Chunk, error) {
	buf := make([]byte, 8)
	n, err := f.reader.Read(buf)
	if n == 0 && err != nil {
		return nil, err
	}
	return &tunnel.Chunk{Content: buf[:n]}, nil
}

func (f *fakeTunnelClient) StreamSnapshotVolumes(
	ctx context.Context, in *tunnel.Empty, opts ...grpc.CallOption,
) (tunnel.Tunnel_StreamSnapshotVolumesClient, error) {
	return &fakeStreamSnapshotVolumesClient{reader: strings.NewReader(f.payload)}, nil
}

func TestPushVolumesFromTunnel(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	client := &fakeTunnelClient{payload: "tar-bytes-stand-in"}

	digest, size, err := PushVolumesFromTunnel(context.Background(), client, host+"/acme/snapshots")
	require.NoError(t, err)
	require.NotEmpty(t, digest)
	require.EqualValues(t, len("tar-bytes-stand-in"), size)

	rc, err := PullBlob(context.Background(), host+"/acme/snapshots", digest)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "tar-bytes-stand-in", string(got))
}

// erroringStreamSnapshotVolumesClient returns one chunk and then a non-EOF
// error, simulating a gRPC failure partway through the tunnel stream.
type erroringStreamSnapshotVolumesClient struct {
	tunnel.Tunnel_StreamSnapshotVolumesClient
	sentFirst bool
}

var errStreamBroken = errors.New("stream broken")

func (f *erroringStreamSnapshotVolumesClient) Recv() (*tunnel.Chunk, error) {
	if !f.sentFirst {
		f.sentFirst = true
		return &tunnel.Chunk{Content: []byte("partial-data")}, nil
	}
	return nil, errStreamBroken
}

type erroringTunnelClient struct {
	tunnel.TunnelClient
}

func (f *erroringTunnelClient) StreamSnapshotVolumes(
	ctx context.Context, in *tunnel.Empty, opts ...grpc.CallOption,
) (tunnel.Tunnel_StreamSnapshotVolumesClient, error) {
	return &erroringStreamSnapshotVolumesClient{}, nil
}

func TestPushVolumesFromTunnel_StreamErrorDoesNotPushTruncatedBlob(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repository := host + "/acme/snapshots"

	client := &erroringTunnelClient{}

	digest, size, err := PushVolumesFromTunnel(context.Background(), client, repository)
	require.Error(t, err)
	require.ErrorIs(t, err, errStreamBroken)
	require.Empty(t, digest)
	require.Zero(t, size)

	// The stream error surfaces while spooling to the temp file, before the
	// digest is computed or remote.WriteLayer is ever called, so no blob
	// upload is attempted: the repository was never even created.
	resp, err := http.Get(srv.URL + "/v2/acme/snapshots/tags/list")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "NAME_UNKNOWN")
}
