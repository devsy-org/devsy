package mcp

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/devsy-org/devsy/pkg/log"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// safeHandler wraps a typed MCP handler so a panic in one tool doesn't kill
// the server. Recovered panics are logged with a stack and returned as a tool error.
func safeHandler[In any, Out any](
	inner func(context.Context, *sdkmcp.CallToolRequest, In) (*sdkmcp.CallToolResult, Out, error),
) func(context.Context, *sdkmcp.CallToolRequest, In) (*sdkmcp.CallToolResult, Out, error) {
	return func(
		ctx context.Context, req *sdkmcp.CallToolRequest, in In,
	) (result *sdkmcp.CallToolResult, out Out, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("mcp handler panic: %v\n%s", r, debug.Stack())
				result = errorResult(fmt.Errorf("handler panicked: %v", r))
				// Reset in case the handler partially populated out before panicking.
				out = *new(Out)
				err = nil
			}
		}()
		return inner(ctx, req, in)
	}
}
