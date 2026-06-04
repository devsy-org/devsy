package mcp

import (
	"context"
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
	serveCmd.registerTools(server)

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
