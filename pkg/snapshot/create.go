package snapshot

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
)

// PushVolumesFromTunnel asks the in-container agent to tar its workspace
// mounts over the existing gRPC tunnel and streams the result into the
// registry as a volumes blob. The stream is spooled to a temp file rather
// than buffered in memory (via PushBlobStreaming), since workspace volume
// tars can be multiple GB.
func PushVolumesFromTunnel(
	ctx context.Context, client tunnel.TunnelClient, repository string,
) (string, int64, error) {
	stream, err := client.StreamSnapshotVolumes(ctx, &tunnel.Empty{})
	if err != nil {
		return "", 0, fmt.Errorf("start snapshot volumes stream: %w", err)
	}

	reader := tunnelserver.NewStreamReader(stream)

	digest, size, err := PushBlobStreaming(ctx, repository, VolumesMediaType, reader)
	if err != nil {
		return "", 0, fmt.Errorf("push snapshot volumes blob: %w", err)
	}
	return digest, size, nil
}
