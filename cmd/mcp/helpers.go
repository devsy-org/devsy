package mcp

import sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

// opResultHandler is the standard (errorResult | opOK) wrapper for void operations.
// It calls fn and returns either an error result or opOK{OK: true}.
func opResultHandler(fn func() error) (*sdkmcp.CallToolResult, opOK, error) {
	if err := fn(); err != nil {
		return errorResult(err), opOK{}, nil
	}
	return nil, opOK{OK: true}, nil
}
