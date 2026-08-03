package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync/atomic"
)

const jsonRPCVersion = "2.0"

// MCPClient drives a real `devsy mcp serve` subprocess over stdio JSON-RPC,
// for e2e tests that need to exercise the actual MCP transport rather than
// the in-process SDK transport used by cmd/mcp's unit tests.
type MCPClient struct {
	cmd     *exec.Cmd
	stdin   *bufio.Writer
	stdout  *bufio.Reader
	nextID  atomic.Int64
	closeFn func() error
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// StartMCPServer launches `devsy mcp serve` as a subprocess and completes the
// MCP initialize handshake. Callers must call Close when done.
func (f *Framework) StartMCPServer(ctx context.Context) (*MCPClient, error) {
	// #nosec G204 -- fixed subcommand args against the compiled test binary, not user input
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), "mcp", "serve")
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start devsy mcp serve: %w", err)
	}

	c := &MCPClient{
		cmd:    cmd,
		stdin:  bufio.NewWriter(stdinPipe),
		stdout: bufio.NewReader(stdoutPipe),
		closeFn: func() error {
			_ = stdinPipe.Close()
			return cmd.Wait()
		},
	}

	if err := c.send(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		ID:      c.nextID.Add(1),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "devsy-e2e", "version": "0.1"},
		},
	}); err != nil {
		return nil, err
	}
	if _, err := c.readResponse(); err != nil {
		return nil, fmt.Errorf("initialize handshake: %w", err)
	}
	if err := c.send(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		Method:  "notifications/initialized",
	}); err != nil {
		return nil, err
	}

	return c, nil
}

// CallTool invokes an MCP tool and returns its decoded structuredContent,
// whether the tool reported isError, and any transport-level error.
func (c *MCPClient) CallTool(
	ctx context.Context, name string, args map[string]any,
) (map[string]any, bool, error) {
	if err := c.send(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		ID:      c.nextID.Add(1),
		Method:  "tools/call",
		Params:  map[string]any{"name": name, "arguments": args},
	}); err != nil {
		return nil, false, err
	}
	resp, err := c.readResponse()
	if err != nil {
		return nil, false, err
	}
	if resp.Error != nil {
		return nil, false, fmt.Errorf("jsonrpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		IsError           bool           `json:"isError"`
		StructuredContent map[string]any `json:"structuredContent"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, false, fmt.Errorf("unmarshal tool result: %w", err)
	}
	return result.StructuredContent, result.IsError, nil
}

// Close terminates the subprocess and releases its pipes.
func (c *MCPClient) Close() error {
	return c.closeFn()
}

func (c *MCPClient) send(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	if err := c.stdin.WriteByte('\n'); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	return c.stdin.Flush()
}

func (c *MCPClient) readResponse() (*jsonRPCResponse, error) {
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response %q: %w", line, err)
	}
	return &resp, nil
}
