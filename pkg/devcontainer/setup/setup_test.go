package setup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
)

// TestSecretMountPath_RejectsEscapes ensures only a plain filename is accepted:
// path separators, "." / ".." and traversal are rejected so a crafted target
// cannot escape SecretsMountDir or write through a symlinked intermediate dir.
func TestSecretMountPath_RejectsEscapes(t *testing.T) {
	valid := []string{"tls.key", "a.b.c", "TOKEN"}
	for _, target := range valid {
		path, err := secretMountPath(target)
		if err != nil {
			t.Errorf("secretMountPath(%q) unexpected error: %v", target, err)
			continue
		}
		if path != filepath.Join(config.SecretsMountDir, target) {
			t.Errorf("secretMountPath(%q) = %q, want %q", target, path,
				filepath.Join(config.SecretsMountDir, target))
		}
	}

	rejected := []string{
		"../evil", "../../etc/cron.d/x", "sub/../../../evil",
		"sub/tls.key", "/etc/passwd", ".", "..", "",
	}
	for _, target := range rejected {
		if path, err := secretMountPath(target); err == nil {
			t.Errorf("secretMountPath(%q) = %q, want rejection", target, path)
		}
	}
}

// fakeTunnelClient is a minimal stub of tunnel.TunnelClient. Only KubeConfig
// is exercised here; the other methods return zero values and are unused by
// setupKubeConfig.
type fakeTunnelClient struct {
	tunnel.TunnelClient // embed for the unused methods (nil panics if called)
	kubeConfigPayload   string
}

// Compile-time guard: a typo in the KubeConfig signature would silently
// fail to override the embedded interface method otherwise.
var _ tunnel.TunnelClient = (*fakeTunnelClient)(nil)

func (f *fakeTunnelClient) KubeConfig(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return &tunnel.Message{Message: f.kubeConfigPayload}, nil
}

// TestSetupKubeConfig_EmptyPayloadSuppressesInfoLog verifies that an empty
// KubeConfig RPC reply does NOT emit the "setup KubeConfig" Info line. The
// e2e substring-absence check is brittle to log renames; this is the
// direct guard for the demotion to Debug.
func TestSetupKubeConfig_EmptyPayloadSuppressesInfoLog(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	client := &fakeTunnelClient{kubeConfigPayload: ""}
	if err := setupKubeConfig(context.Background(), nil, client); err != nil {
		t.Fatalf("setupKubeConfig: %v", err)
	}

	for _, entry := range logs.All() {
		if entry.Level >= zapcore.InfoLevel {
			// "setup KubeConfig" specifically must not appear at Info+.
			if entry.Message == "setup KubeConfig" {
				t.Errorf("expected no 'setup KubeConfig' log; got: %+v", entry)
			}
		}
	}
	if got := logs.FilterMessageSnippet("setup KubeConfig").Len(); got != 0 {
		t.Errorf("expected zero 'setup KubeConfig' entries, got %d", got)
	}
}

// TestSetupKubeConfig_NonEmptyPayloadEmitsInfoLog asserts the Info-level
// "setup KubeConfig" line still fires when the host returns a non-empty
// kubeconfig payload. writeKubeConfig may fail in the test environment, but
// the log is emitted BEFORE that call so we still expect to see it.
func TestSetupKubeConfig_NonEmptyPayloadEmitsInfoLog(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	client := &fakeTunnelClient{
		kubeConfigPayload: "apiVersion: v1\nclusters: []",
	}
	// Error is expected because writeKubeConfig will fail without a valid
	// remote user / home dir in the test env; we only care about the log.
	_ = setupKubeConfig(context.Background(), nil, client)

	if got := logs.FilterMessage("setup KubeConfig").Len(); got == 0 {
		t.Errorf(
			"expected at least one 'setup KubeConfig' log entry on non-empty payload, got 0 (all=%v)",
			logs.All(),
		)
	}
}

// TestWriteResultFileTo_ProducesWorldReadableFile guards the fix for a
// SSH-session-user footgun: getContainerResult and portOptionsFromResult
// read this file over sessions that may authenticate as root or the
// workspace's remoteUser, so it must not be locked to whichever user's
// process happened to create it first.
func TestWriteResultFileTo_ProducesWorldReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "result.json")

	if err := writeResultFileTo(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("writeResultFileTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %o, want 0644 (must be readable by any container user)", got)
	}
}

// TestWriteResultFileTo_SkipsWriteWhenContentUnchanged locks in the
// no-op-on-unchanged-content behavior so frequent writeResultFile calls
// during setup don't repeatedly touch the file's mtime/mode for no reason.
func TestWriteResultFileTo_SkipsWriteWhenContentUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	content := []byte(`{"ok":true}`)

	if err := writeResultFileTo(path, content); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := writeResultFileTo(path, content); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if !second.ModTime().Equal(first.ModTime()) {
		t.Errorf(
			"mtime changed on unchanged content: first=%v second=%v",
			first.ModTime(), second.ModTime(),
		)
	}
}

// TestWriteResultFileTo_WidensExistingRestrictiveMode ensures a file left
// behind by a pre-fix binary (0600) gets corrected on the next write, not
// just newly-created files.
func TestWriteResultFileTo_WidensExistingRestrictiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	// #nosec G306 -- intentional: simulating a pre-fix file left at 0600
	if err := os.WriteFile(path, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := writeResultFileTo(path, []byte(`{"new":true}`)); err != nil {
		t.Fatalf("writeResultFileTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %o, want 0644 (a stale 0600 file must be widened)", got)
	}
}

// TestWriteResultFileTo_WidensStaleModeEvenWhenContentUnchanged guards
// against the unchanged-content early return skipping the widen step: a
// file at a stale restrictive mode must get corrected on the next call
// even if the content it's writing happens to already match.
func TestWriteResultFileTo_WidensStaleModeEvenWhenContentUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	content := []byte(`{"ok":true}`)
	// #nosec G306 -- intentional: simulating a pre-fix file left at 0600
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := writeResultFileTo(path, content); err != nil {
		t.Fatalf("writeResultFileTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %o, want 0644 even though content was already up to date", got)
	}
}
