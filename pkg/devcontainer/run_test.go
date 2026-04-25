package devcontainer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/types"
)

func TestRunInitializeCommand_ParallelTiming(t *testing.T) {
	tmpDir := t.TempDir()
	conf := &config.DevContainerConfig{}
	conf.InitializeCommand = types.LifecycleHook{
		"sleep-one": {"sleep", "0.5"},
		"sleep-two": {"sleep", "0.5"},
	}

	start := time.Now()
	err := runInitializeCommand(tmpDir, conf, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed >= 900*time.Millisecond {
		t.Fatalf("expected parallel execution under 900ms, took %s", elapsed)
	}
}

func TestRunInitializeCommand_ParallelErrorCollection(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "success.out")

	conf := &config.DevContainerConfig{}
	conf.InitializeCommand = types.LifecycleHook{
		"will-fail":    {"sh", "-c", "exit 1"},
		"will-succeed": {"sh", "-c", "echo -n ok > " + markerFile},
	}

	err := runInitializeCommand(tmpDir, conf, nil)
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !contains(err.Error(), "will-fail") {
		t.Fatalf("error should mention 'will-fail', got: %v", err)
	}

	data, readErr := os.ReadFile(markerFile) //nolint:gosec // G304 — test temp file
	if readErr != nil {
		t.Fatalf("success marker not written; parallel commands should all run: %v", readErr)
	}
	if string(data) != "ok" {
		t.Fatalf("expected marker content 'ok', got %q", string(data))
	}
}

func TestRunInitializeCommand_SingleKey(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "single.out")

	conf := &config.DevContainerConfig{}
	conf.InitializeCommand = types.LifecycleHook{
		"write-file": {"sh", "-c", "echo -n single > " + outFile},
	}

	err := runInitializeCommand(tmpDir, conf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(outFile) //nolint:gosec // G304 — test temp file
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if string(data) != "single" {
		t.Fatalf("expected 'single', got %q", string(data))
	}
}

func TestRunInitializeCommand_StringFormat(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "string.out")

	conf := &config.DevContainerConfig{}
	// String format produces a single anonymous key with one-element slice.
	conf.InitializeCommand = types.LifecycleHook{
		"": {"echo -n stringfmt > " + outFile},
	}

	err := runInitializeCommand(tmpDir, conf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(outFile) //nolint:gosec // G304 — test temp file
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if string(data) != "stringfmt" {
		t.Fatalf("expected 'stringfmt', got %q", string(data))
	}
}

func TestRunInitializeCommand_Empty(t *testing.T) {
	// nil config
	conf := &config.DevContainerConfig{}
	if err := runInitializeCommand(t.TempDir(), conf, nil); err != nil {
		t.Fatalf("nil InitializeCommand should return nil, got: %v", err)
	}

	// empty map
	conf.InitializeCommand = types.LifecycleHook{}
	if err := runInitializeCommand(t.TempDir(), conf, nil); err != nil {
		t.Fatalf("empty InitializeCommand should return nil, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
