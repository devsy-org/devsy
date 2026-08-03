package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
)

const jsonRPCVersion = "2.0"

// MCPClient drives a real `devsy mcp serve` subprocess over stdio JSON-RPC.
// Not safe for concurrent CallTool calls: mu serializes the send+read round
// trip so one call can't read another's response off the shared stream. If a
// call's ctx is cancelled while its read is in flight, the client is
// poisoned (see readResponseForCtx) and every later call fails fast.
type MCPClient struct {
	cmd      *exec.Cmd
	stdin    *bufio.Writer
	stdout   *bufio.Reader
	mu       sync.Mutex
	nextID   int64
	poisoned bool
	closeFn  func() error
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// StartMCPServer launches `devsy mcp serve` and completes the MCP initialize
// handshake. Callers must call Close when done.
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

	cleanup := func() {
		_ = stdinPipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
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

	initID := c.nextRequestID()
	if err := c.send(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		ID:      initID,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "devsy-e2e", "version": "0.1"},
		},
	}); err != nil {
		cleanup()
		return nil, err
	}
	if _, err := c.readResponseFor(initID); err != nil {
		cleanup()
		return nil, fmt.Errorf("initialize handshake: %w", err)
	}
	if err := c.send(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		Method:  "notifications/initialized",
	}); err != nil {
		cleanup()
		return nil, err
	}

	return c, nil
}

func (c *MCPClient) CallTool(
	ctx context.Context, name string, args map[string]any,
) (map[string]any, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.poisoned {
		return nil, false, fmt.Errorf(
			"mcp client unusable after a prior call's context was cancelled mid-read",
		)
	}

	id := c.nextRequestID()
	if err := c.send(jsonRPCRequest{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  "tools/call",
		Params:  map[string]any{"name": name, "arguments": args},
	}); err != nil {
		return nil, false, err
	}
	resp, err := c.readResponseForCtx(ctx, id)
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

func (c *MCPClient) Close() error {
	return c.closeFn()
}

// nextRequestID must only be called while holding c.mu, except during
// StartMCPServer's handshake before the client is returned to any caller.
func (c *MCPClient) nextRequestID() int64 {
	c.nextID++
	return c.nextID
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

// readResponseFor reads and discards notifications (no id) until it finds
// the response matching id — tool calls that stream log progress interleave
// notifications with the eventual response on the same stream.
func (c *MCPClient) readResponseFor(id int64) (*jsonRPCResponse, error) {
	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal response %q: %w", line, err)
		}
		if resp.ID == nil || *resp.ID != id {
			continue
		}
		return &resp, nil
	}
}

// readResponseForCtx races readResponseFor against ctx so a hung server
// can't block the caller past its deadline. bufio.Reader isn't safe for
// concurrent use, so a cancellation that fires while the read is still in
// flight leaves that goroutine's read outstanding on c.stdout — the client
// is marked poisoned and every later call on it fails fast instead of
// risking a second concurrent read.
func (c *MCPClient) readResponseForCtx(ctx context.Context, id int64) (*jsonRPCResponse, error) {
	type result struct {
		resp *jsonRPCResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := c.readResponseFor(id)
		done <- result{resp, err}
	}()

	select {
	case r := <-done:
		return r.resp, r.err
	case <-ctx.Done():
		c.poisoned = true
		return nil, fmt.Errorf("waiting for response to request %d: %w", id, ctx.Err())
	}
}
