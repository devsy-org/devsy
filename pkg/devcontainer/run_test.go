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
		"will-succeed": {"sh", "-c", "printf ok > " + markerFile},
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
		"write-file": {"sh", "-c", "printf single > " + outFile},
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
		"": {"printf stringfmt > " + outFile},
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

func TestGetWorkspace_CustomWorkspaceMount(t *testing.T) {
	customMount := "type=bind,source=/host/src,target=/custom-ws"
	conf := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			WorkspaceMount: customMount,
		},
	}

	mount, folder := getWorkspace("/ignored", "ws-id", conf)

	if !contains(mount, customMount) {
		t.Fatalf("expected workspaceMount string to contain original, got %q", mount)
	}
	if folder != "/custom-ws" {
		t.Fatalf("expected target /custom-ws, got %q", folder)
	}
}

func TestGetWorkspace_DefaultMount(t *testing.T) {
	conf := &config.DevContainerConfig{}

	mount, folder := getWorkspace("/home/user/project", "abc123", conf)

	if !contains(mount, "type=bind") || !contains(mount, "source=/home/user/project") {
		t.Fatalf("expected bind mount with source, got %q", mount)
	}
	if !contains(mount, "target=/workspaces/abc123") {
		t.Fatalf("expected target /workspaces/abc123, got %q", mount)
	}
	if folder != "/workspaces/abc123" {
		t.Fatalf("expected /workspaces/abc123, got %q", folder)
	}
}

func TestGetWorkspace_DefaultMountWithWorkspaceFolder(t *testing.T) {
	conf := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			WorkspaceFolder: "/app",
		},
	}

	mount, folder := getWorkspace("/home/user/project", "ws-id", conf)

	if !contains(mount, "type=bind") || !contains(mount, "target=/app") {
		t.Fatalf("expected bind mount with target /app, got %q", mount)
	}
	if folder != "/app" {
		t.Fatalf("expected /app, got %q", folder)
	}
}

func TestGetWorkspace_EmptyWorkspaceMount(t *testing.T) {
	conf := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			WorkspaceMount: "",
		},
	}

	mount, folder := getWorkspace("/home/user/project", "ws-id", conf)

	if folder != "/workspaces/ws-id" {
		t.Fatalf("expected default folder, got %q", folder)
	}
	if !contains(mount, "type=bind") {
		t.Fatalf("expected default bind mount, got %q", mount)
	}
}

func TestGetWorkspace_UserConsistencyPreserved(t *testing.T) {
	customMount := "type=bind,source=/src,target=/ws,consistency=delegated"
	conf := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			WorkspaceMount: customMount,
		},
	}

	mount, _ := getWorkspace("/ignored", "ws-id", conf)

	if !contains(mount, "consistency=delegated") {
		t.Fatalf("expected user consistency=delegated preserved, got %q", mount)
	}
	if contains(mount, "consistency='consistent'") {
		t.Fatalf(
			"default consistency should not be appended when user specifies one, got %q",
			mount,
		)
	}
}

func TestMountHasConsistency(t *testing.T) {
	tests := []struct {
		mount string
		want  bool
	}{
		{"type=bind,source=/s,target=/t,consistency=cached", true},
		{"type=bind,source=/s,target=/t,consistency='consistent'", true},
		{"type=bind,source=/s,target=/t", false},
	}
	for _, tt := range tests {
		if got := mountHasConsistency(tt.mount); got != tt.want {
			t.Errorf("mountHasConsistency(%q) = %v, want %v", tt.mount, got, tt.want)
		}
	}
}

func TestMountSetConsistency(t *testing.T) {
	tests := []struct {
		name  string
		mount string
		value string
		want  string
	}{
		{
			name:  "appends when no consistency present",
			mount: "type=bind,source=/s,target=/t",
			value: "delegated",
			want:  "type=bind,source=/s,target=/t,consistency='delegated'",
		},
		{
			name:  "replaces existing single-quoted default",
			mount: "type=bind,source=/s,target=/t,consistency='consistent'",
			value: "delegated",
			want:  "type=bind,source=/s,target=/t,consistency='delegated'",
		},
		{
			name:  "replaces existing unquoted value",
			mount: "type=bind,source=/s,target=/t,consistency=cached",
			value: "delegated",
			want:  "type=bind,source=/s,target=/t,consistency='delegated'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mountSetConsistency(tt.mount, tt.value)
			if got != tt.want {
				t.Errorf(
					"mountSetConsistency(%q, %q) =\n  %q\nwant:\n  %q",
					tt.mount,
					tt.value,
					got,
					tt.want,
				)
			}
		})
	}
}
