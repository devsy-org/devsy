package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
)

func TestGetWorkspaceClient_UnknownNameReturnsError(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))

	client, err := getWorkspaceClient(context.Background(), &flags.GlobalFlags{}, "missing-ws")
	if err == nil {
		t.Fatalf("getWorkspaceClient for an unknown workspace name must return an error")
	}
	if client != nil {
		t.Fatalf("getWorkspaceClient for an unknown workspace name must return a nil client")
	}
}
