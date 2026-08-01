package tunnelserver

import (
	"errors"
	"io"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/log"
)

// NewStreamReader adapts stream into an io.Reader. A non-EOF error from
// stream.Recv() propagates to the reader via CloseWithError rather than
// being silently converted to a clean io.EOF: masking it as EOF would let a
// mid-stream failure (e.g. a dropped gRPC connection) look like a
// successful, but truncated, transfer to whatever is consuming the reader
// (extract.Extract for StreamWorkspace/StreamMount, and the snapshot
// volumes blob push). This is shared by every Tunnel_StreamWorkspaceClient
// consumer, not just snapshots — see stream_test.go for coverage of both.
func NewStreamReader(stream tunnel.Tunnel_StreamWorkspaceClient) io.Reader {
	reader, writer := io.Pipe()

	go func() {
		for {
			resp, err := stream.Recv()
			if resp != nil && len(resp.Content) > 0 {
				if _, werr := writer.Write(resp.Content); werr != nil {
					log.Debugf("Error writing to pipe: %v", werr)
					_ = writer.CloseWithError(werr)
					return
				}
			}
			if errors.Is(err, io.EOF) {
				_ = writer.Close()
				return
			} else if err != nil {
				log.Debugf("Error receiving from stream: %v", err)
				_ = writer.CloseWithError(err)
				return
			}
		}
	}()

	return reader
}

func NewStreamWriter(stream tunnel.Tunnel_StreamWorkspaceServer) io.Writer {
	return &streamWriter{stream: stream, lastMessage: time.Now()}
}

type streamWriter struct {
	stream tunnel.Tunnel_StreamWorkspaceServer

	lastMessage  time.Time
	bytesWritten int64
}

func (s *streamWriter) Write(p []byte) (int, error) {
	err := s.stream.Send(&tunnel.Chunk{Content: p})
	if err != nil {
		return 0, err
	}

	s.bytesWritten += int64(len(p))
	if time.Since(s.lastMessage) > time.Second*2 {
		log.Infof("Uploaded %.2f MB", float64(s.bytesWritten)/1024/1024)
		s.lastMessage = time.Now()
	}

	return len(p), nil
}
