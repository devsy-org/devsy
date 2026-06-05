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
				// Return a non-nil Go error so the SDK takes over with SetError.
				// Returning a custom result here would lose its StructuredContent
				// when the SDK marshals the (zero) typed Out over it.
				result = nil
				out = *new(Out)
				err = fmt.Errorf("handler panicked: %v", r)
			}
		}()
		return inner(ctx, req, in)
	}
}
