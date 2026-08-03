package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServer_ListsAllTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSY_HOME", home)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "devsy-test", Version: "test"}, nil)
	g := &flags.GlobalFlags{}
	serveCmd := &ServeCmd{GlobalFlags: g, ExecOutputCap: 1024}
	serveCmd.registerTools(server, newOpSemaphore(8))

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx, serverTransport)
	}()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	wantNames := []string{
		"workspace_list", "workspace_status", "workspace_start", "workspace_stop",
		"workspace_delete", "workspace_create", "workspace_exec",
		"provider_list", "provider_add", "provider_delete", "provider_use",
	}
	have := map[string]bool{}
	for _, tool := range tools.Tools {
		have[tool.Name] = true
	}
	for _, name := range wantNames {
		if !have[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
	if len(tools.Tools) != len(wantNames) {
		t.Errorf("expected %d tools, got %d: %+v", len(wantNames), len(tools.Tools), have)
	}
}

func TestServer_WorkspaceExecRespectsSemaphore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSY_HOME", home)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "devsy-test", Version: "test"}, nil)
	g := &flags.GlobalFlags{}
	serveCmd := &ServeCmd{GlobalFlags: g, ExecOutputCap: 1024, MaxConcurrentOps: 1}
	sem := newOpSemaphore(serveCmd.MaxConcurrentOps)
	serveCmd.registerTools(server, sem)

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx, serverTransport)
	}()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	execArgs := map[string]any{
		"name":    "some-workspace",
		"command": []string{"echo", "hi"},
	}

	release, err := sem.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	blockedCtx, blockedCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer blockedCancel()
	_, callErr := session.CallTool(blockedCtx, &sdkmcp.CallToolParams{
		Name:      "workspace_exec",
		Arguments: execArgs,
	})
	if callErr == nil {
		t.Fatal("expected workspace_exec call to fail while the only semaphore slot is held")
	}

	release()

	res, callErr := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "workspace_exec",
		Arguments: execArgs,
	})
	if callErr != nil {
		t.Fatalf(
			"expected workspace_exec call to reach the handler after release, got transport error: %v",
			callErr,
		)
	}
	if res.IsError {
		var msg string
		for _, c := range res.Content {
			if tc, ok := c.(*sdkmcp.TextContent); ok {
				msg = tc.Text
			}
		}
		if strings.Contains(msg, "waiting for a free operation slot") {
			t.Fatalf("workspace_exec failed due to the semaphore even after release: %s", msg)
		}
	}
}
