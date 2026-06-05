package mcp

import (
	"bufio"
	"context"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/log"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// streamLogsToSession runs fn while forwarding every pkg/log line produced
// by it (or any goroutine using the global logger) to the MCP client as a
// logging notification. The session lets clients display progress for
// long-running tools instead of seeing a single opaque pause.
//
// Lines from goroutines unrelated to fn are also forwarded for the duration
// — acceptable because the only long-running concurrent work the MCP server
// kicks off is the call itself.
func streamLogsToSession(
	ctx context.Context,
	session *sdkmcp.ServerSession,
	fn func() error,
) error {
	reader, writer := io.Pipe()
	removeSink := log.AddSink(writer)

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r\n")
			if line == "" {
				continue
			}
			_ = session.Log(ctx, &sdkmcp.LoggingMessageParams{
				Level:  "info",
				Logger: "devsy",
				Data:   line,
			})
		}
	}()

	err := fn()

	removeSink()
	_ = writer.Close()
	<-done
	_ = reader.Close()
	return err
}
