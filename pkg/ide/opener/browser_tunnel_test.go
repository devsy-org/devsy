package opener

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/tunnel"
)

// containsAdjacent returns true if args contains needle followed immediately
// by value.
func containsAdjacent(args []string, needle, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == needle && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsArg(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestBuildHelperArgs_Basic(t *testing.T) {
	args := buildHelperArgs("ctx-test", "ws-test", tunnel.BrowserTunnelParams{
		TargetURL:        "http://localhost:10800/?folder=/workspace",
		AuthSockID:       "sock-abc",
		User:             "test-user",
		GitSSHSigningKey: "",
	})

	if len(args) < 2 || args[0] != "helper" || args[1] != "browser-tunnel" {
		t.Fatalf("expected args to start with [helper browser-tunnel], got %v", args)
	}

	checkPairs := []struct {
		flag, value string
	}{
		{"--context", "ctx-test"},
		{"--workspace", "ws-test"},
		{"--target-url", "http://localhost:10800/?folder=/workspace"},
		{"--auth-sock-id", "sock-abc"},
		{"--user", "test-user"},
	}
	for _, p := range checkPairs {
		if !containsAdjacent(args, p.flag, p.value) {
			t.Errorf("missing %s %s in %v", p.flag, p.value, args)
		}
	}

	for _, unwanted := range []string{"--forward-ports", "--extra-ports"} {
		if containsArg(args, unwanted) {
			t.Errorf("unexpected %s for default params: %v", unwanted, args)
		}
	}
}

func TestBuildHelperArgs_ForwardPorts(t *testing.T) {
	args := buildHelperArgs("ctx", "ws", tunnel.BrowserTunnelParams{
		TargetURL:    "http://localhost:1234",
		ForwardPorts: true,
	})
	if !containsArg(args, "--forward-ports") {
		t.Errorf("expected --forward-ports in %v", args)
	}
}

func TestBuildHelperArgs_ExtraPorts(t *testing.T) {
	args := buildHelperArgs("ctx", "ws", tunnel.BrowserTunnelParams{
		TargetURL:  "http://localhost:1234",
		ExtraPorts: []string{"localhost:10800", "127.0.0.1:8443"},
	})
	if !containsAdjacent(args, "--extra-ports", "localhost:10800") {
		t.Errorf("missing --extra-ports localhost:10800 in %v", args)
	}
	if !containsAdjacent(args, "--extra-ports", "127.0.0.1:8443") {
		t.Errorf("missing --extra-ports 127.0.0.1:8443 in %v", args)
	}
}

// setupTempHome redirects the path manager to a temp HOME so the workspace
// dir is writable and isolated from the real user's devsy data.
func setupTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func TestWriteReadTunnelState_RoundTrip(t *testing.T) {
	setupTempHome(t)

	want := TunnelState{
		PID:        12345,
		CreateTime: 67890,
		TargetURL:  "http://localhost:10800",
		Label:      LabelVSCodeBrowser,
	}
	if err := WriteTunnelState("ctx-test", "ws-test", want); err != nil {
		t.Fatalf("WriteTunnelState: %v", err)
	}

	got, err := ReadTunnelState("ctx-test", "ws-test")
	if err != nil {
		t.Fatalf("ReadTunnelState: %v", err)
	}
	if got == nil {
		t.Fatal("ReadTunnelState returned nil after WriteTunnelState")
	}
	if *got != want {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", *got, want)
	}

	assertStatePathSane(t, "ctx-test", "ws-test")
}

func assertStatePathSane(t *testing.T, contextName, workspaceID string) {
	t.Helper()
	statePath, err := TunnelStateFilePath(contextName, workspaceID)
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	if !strings.HasPrefix(statePath, os.Getenv("HOME")) {
		t.Errorf("statePath %q is not under HOME %q", statePath, os.Getenv("HOME"))
	}
	if filepath.Base(statePath) != TunnelStateFileName {
		t.Errorf("statePath basename = %q, want %q", filepath.Base(statePath), TunnelStateFileName)
	}
}

func TestReadTunnelState_MissingReturnsNilNil(t *testing.T) {
	setupTempHome(t)

	state := TunnelState{PID: 42, CreateTime: 1, TargetURL: "http://x", Label: LabelVSCodeBrowser}
	if err := WriteTunnelState("ctx-test", "ws-test", state); err != nil {
		t.Fatalf("WriteTunnelState: %v", err)
	}
	statePath, err := TunnelStateFilePath("ctx-test", "ws-test")
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove state file: %v", err)
	}

	got, err := ReadTunnelState("ctx-test", "ws-test")
	if err != nil {
		t.Fatalf("ReadTunnelState after remove: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil state after remove, got %+v", *got)
	}
}
