package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
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

func TestHasLocalOverride_EnvOverrideSet(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "devsy-linux-arm64")
	if err := os.WriteFile(binPath, []byte("agent-bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv(config.EnvAgentBinary, binPath)

	mgr := &BinaryManager{}
	if !mgr.HasLocalOverride("some-other-arch") {
		t.Error(
			"HasLocalOverride() = false, want true when DEVSY_AGENT_BINARY is set (regardless of arch)",
		)
	}
}

func TestHasLocalOverride_MatchingHostArch(t *testing.T) {
	t.Setenv(config.EnvAgentBinary, "")
	mgr := &BinaryManager{}

	if runtime.GOOS != osLinux {
		t.Skip("this host cannot supply a linux binary; matching-arch case not applicable")
	}
	if !mgr.HasLocalOverride(runtime.GOARCH) {
		t.Error("HasLocalOverride() = false, want true when the host's own arch matches the target")
	}
}

func TestHasLocalOverride_NoOverrideNoArchMatch(t *testing.T) {
	t.Setenv(config.EnvAgentBinary, "")
	mgr := &BinaryManager{}

	if mgr.HasLocalOverride("definitely-not-a-real-arch") {
		t.Error(
			"HasLocalOverride() = true, want false with no env override and no matching host arch",
		)
	}
}
