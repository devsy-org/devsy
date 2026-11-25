//go:build !windows

package apple

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAppleMasksReturnedFailureOutput(t *testing.T) {
	const foldedValue = "synthetic-apple-command-folded-value"
	const registeredValue = "synthetic-apple-command-registered-value"
	const detail = "public Apple command detail"
	const output = "Authorization:\n Bearer " + foldedValue + "\n\n" + detail + " " + registeredValue
	helper := &AppleHelper{
		Command:     stubContainer(t, output, 23),
		Environment: []string{"DEVSY_APPLE_TEST_TOKEN=" + registeredValue},
	}
	for _, tc := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"system", helper.EnsureSystemRunning},
		{"builder", helper.EnsureBuilderRunning},
		{"start", func(ctx context.Context) error { return helper.StartContainer(ctx, "fixture-container") }},
		{"stop", func(ctx context.Context) error { return helper.Stop(ctx, "fixture-container") }},
		{"remove", func(ctx context.Context) error { return helper.Remove(ctx, "fixture-container") }},
		{"tag", func(ctx context.Context) error { return helper.Tag(ctx, "fixture-image", "fixture-tag") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t.Context())
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			require.Equal(t, 23, exitErr.ExitCode())
			require.Contains(t, err.Error(), detail)
			require.NotContains(t, err.Error(), foldedValue)
			require.NotContains(t, err.Error(), registeredValue)
		})
	}
}

func TestMatchesLabels(t *testing.T) {
	labels := map[string]string{"dev.containers.id": "ws-1", "k": "v"}
	cases := []struct {
		name      string
		selectors []string
		want      bool
	}{
		{"empty selectors match", nil, true},
		{"single match", []string{"dev.containers.id=ws-1"}, true},
		{"all match", []string{"dev.containers.id=ws-1", "k=v"}, true},
		{"value mismatch", []string{"dev.containers.id=other"}, false},
		{"missing key", []string{"absent=1"}, false},
		{"empty value required but present", []string{"k="}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesLabels(labels, c.selectors); got != c.want {
				t.Errorf("matchesLabels(%v) = %v, want %v", c.selectors, got, c.want)
			}
		})
	}
}

// stubContainer writes a fake `container` executable that echoes a canned stdout
// and exits with the given code, so helper command construction/parsing can be
// tested without the real CLI.
func stubContainer(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "container")
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\nexit " + strconv.Itoa(exitCode) + "\n"
	//nolint:gosec // G306: a test stub must be executable
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestGetImageTag(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		wantTag string
		wantErr bool
	}{
		{
			name:    "tag parsed from reference",
			stdout:  `[{"id":"abc","configuration":{"name":"docker.io/library/alpine:3.20"}}]`,
			wantTag: "3.20",
		},
		{
			name:    "no colon yields empty tag",
			stdout:  `[{"id":"abc","configuration":{"name":"alpine"}}]`,
			wantTag: "",
		},
		{
			name:    "registry port is not mistaken for the tag",
			stdout:  `[{"id":"abc","configuration":{"name":"localhost:5000/library/alpine:3.20"}}]`,
			wantTag: "3.20",
		},
		{
			name:    "registry port with no tag yields empty",
			stdout:  `[{"id":"abc","configuration":{"name":"localhost:5000/library/alpine"}}]`,
			wantTag: "",
		},
		{
			name:    "empty array yields empty tag",
			stdout:  `[]`,
			wantTag: "",
		},
		{
			name:    "invalid json is an error, not a false empty tag",
			stdout:  `not json`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &AppleHelper{Command: stubContainer(t, c.stdout, 0)}
			got, err := h.GetImageTag(context.Background(), "img")
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got tag %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.wantTag {
				t.Errorf("GetImageTag = %q, want %q", got, c.wantTag)
			}
		})
	}
}

func TestParseImageTag(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"docker.io/library/alpine:1.2", "1.2"},
		{"alpine", ""},
		{"localhost:5000/library/alpine:v1", "v1"},
		{"localhost:5000/library/alpine", ""},
		{"alpine@sha256:deadbeef", ""},
		{"alpine:2.1@sha256:deadbeef", "2.1"},
	}
	for _, c := range cases {
		if got := parseImageTag(c.ref); got != c.want {
			t.Errorf("parseImageTag(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestWaitContainerRunningFailsFastOnExit(t *testing.T) {
	// A container reporting a terminal (stopped->exited) state must error
	// immediately rather than block for the full poll timeout.
	stdout := `[{"id":"c1","configuration":{"id":"c1"},"status":{"state":"stopped"}}]`
	h := &AppleHelper{Command: stubContainer(t, stdout, 0)}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	err := h.WaitContainerRunning(ctx, "c1")
	if err == nil {
		t.Fatal("expected an error for an exited container")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("WaitContainerRunning did not fail fast: took %s", elapsed)
	}
}

func TestEnsureBuilderRunning(t *testing.T) {
	// Exit 0 (the real CLI's behavior even when already running) -> no error.
	okHelper := &AppleHelper{Command: stubContainer(t, "", 0)}
	if err := okHelper.EnsureBuilderRunning(context.Background()); err != nil {
		t.Errorf("exit 0 must succeed, got %v", err)
	}

	// A genuine non-zero failure must propagate (no longer swallowed).
	failHelper := &AppleHelper{Command: stubContainer(t, "boom: cannot start", 1)}
	if err := failHelper.EnsureBuilderRunning(context.Background()); err == nil {
		t.Error("non-zero builder start must return an error")
	}

	// A non-zero exit that reports "already running" is tolerated.
	alreadyHelper := &AppleHelper{Command: stubContainer(t, "builder already running", 1)}
	if err := alreadyHelper.EnsureBuilderRunning(context.Background()); err != nil {
		t.Errorf("already-running must be tolerated, got %v", err)
	}
}

func TestAppleCommandDiagnosticMasksJSONCredentialRecordBeforePublicText(t *testing.T) {
	const canary = "synthetic-apple-cookie"
	cmd := exec.Command("container", "list")
	out := []byte(`{"Cookie":"` + canary + `"}` + "\n" + "public apple detail")
	got := appleCommandDiagnostic(cmd, out)
	if strings.Contains(got, canary) || !strings.Contains(got, "public apple detail") {
		t.Fatalf("unexpected diagnostic %q", got)
	}
}
