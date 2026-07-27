package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvPathSourceUsesLocalBinary(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "devsy-linux-arm64")
	if err := os.WriteFile(binPath, []byte("agent-bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("DEVSY_TEST_AGENT_BIN", binPath)

	src := &EnvPathSource{EnvVar: "DEVSY_TEST_AGENT_BIN"}
	rc, err := src.GetBinary(context.Background(), "arm64")
	if err != nil {
		t.Fatalf("GetBinary: %v", err)
	}
	defer func() { _ = rc.Close() }()

	data, _ := io.ReadAll(rc)
	if string(data) != "agent-bytes" {
		t.Errorf("got %q, want the local binary contents", data)
	}
}

func TestEnvPathSourceUnsetFallsThrough(t *testing.T) {
	src := &EnvPathSource{EnvVar: "DEVSY_TEST_AGENT_BIN_UNSET"}
	if _, err := src.GetBinary(context.Background(), "arm64"); err == nil {
		t.Fatal("expected an error when the env var is unset so later sources are tried")
	}
}
