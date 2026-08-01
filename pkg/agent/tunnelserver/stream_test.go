package tunnelserver

import (
	"errors"
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/stretchr/testify/require"
)

// fakeStreamWorkspaceClient is a minimal tunnel.Tunnel_StreamWorkspaceClient
// double that only implements Recv, since NewStreamReader is the only
// consumer under test here.
type fakeStreamWorkspaceClient struct {
	tunnel.Tunnel_StreamWorkspaceClient
	chunks []*tunnel.Chunk
	err    error
	idx    int
}

func (f *fakeStreamWorkspaceClient) Recv() (*tunnel.Chunk, error) {
	if f.idx < len(f.chunks) {
		c := f.chunks[f.idx]
		f.idx++
		return c, nil
	}
	return nil, f.err
}

func TestNewStreamReader_CleanEOFYieldsFullContentAndNoError(t *testing.T) {
	client := &fakeStreamWorkspaceClient{
		chunks: []*tunnel.Chunk{{Content: []byte("hello ")}, {Content: []byte("world")}},
		err:    io.EOF,
	}

	got, err := io.ReadAll(NewStreamReader(client))
	require.NoError(t, err)
	require.Equal(t, "hello world", string(got))
}

// TestNewStreamReader_NonEOFErrorPropagatesInsteadOfMaskingAsEOF guards
// against a regression to the previous behavior, where any stream.Recv()
// error (not just io.EOF) was converted to a clean io.EOF for the reader —
// silently treating a truncated, failed transfer as a complete one. This is
// shared by every Tunnel_StreamWorkspaceClient consumer: StreamWorkspace
// (cmd/internal/agentworkspace/up.go), StreamMount
// (cmd/internal/agentcontainer/setup.go), and the snapshot volumes push
// (pkg/snapshot/create.go) — not just the snapshot feature.
func TestNewStreamReader_NonEOFErrorPropagatesInsteadOfMaskingAsEOF(t *testing.T) {
	streamErr := errors.New("stream broken")
	client := &fakeStreamWorkspaceClient{
		chunks: []*tunnel.Chunk{{Content: []byte("partial-data")}},
		err:    streamErr,
	}

	got, err := io.ReadAll(NewStreamReader(client))
	require.ErrorIs(t, err, streamErr)
	require.Equal(t, "partial-data", string(got))
}
