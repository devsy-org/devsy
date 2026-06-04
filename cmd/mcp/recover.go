package mcp

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/devsy-org/devsy/pkg/log"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// safeHandler wraps a typed MCP handler with panic recovery so a single broken
// tool cannot tear down the server. A recovered panic is logged with a stack
// trace and returned as a structured tool error.
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
				// The panic may have left out half-populated; force the zero
				// value so callers never see a partial result alongside an error.
				out = *new(Out)
				err = nil
			}
		}()
		return inner(ctx, req, in)
	}
}
