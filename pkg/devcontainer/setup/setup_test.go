package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
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

// removeMarkerFile deletes a marker file if present, treating an existing
// leftover from an earlier interrupted run the same as no marker at all.
func removeMarkerFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove marker %s: %v", path, err)
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("markers live under /var/devsy; writing them needs root")
	}
	markerPath := filepath.Join(pkgconfig.ContainerDataDir, "testmarker.marker")
	removeMarkerFile(t, markerPath)
	t.Cleanup(func() { _ = os.Remove(markerPath) })

	exists, err := markerExists("testmarker", "ws-1")
	if err != nil {
		t.Fatalf("markerExists on miss: %v", err)
	}
	if exists {
		t.Fatal("markerExists = true before writeMarker, want false")
	}

	if err := writeMarker("testmarker", "ws-1"); err != nil {
		t.Fatalf("writeMarker: %v", err)
	}

	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{name: "matching content", content: "ws-1", want: true},
		{name: "mismatched content", content: "ws-2", want: false},
		{name: "empty content matches any", content: "", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := markerExists("testmarker", tc.content)
			if err != nil {
				t.Fatalf("markerExists: %v", err)
			}
			if got != tc.want {
				t.Errorf("markerExists(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestChownWorkspaceSkipsAbsentFolder(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("markers live under /var/devsy; writing them needs root")
	}
	t.Setenv(pkgconfig.EnvWorkspaceID, "ws-absent")
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(pkgconfig.ContainerDataDir, "chownWorkspace.marker"))
	})

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: filepath.Join(t.TempDir(), "missing"),
		},
	}

	for i := range 2 {
		if err := chownWorkspace(result, true); err != nil {
			t.Fatalf("chownWorkspace absent folder (call %d): %v", i, err)
		}
	}

	exists, err := markerExists("chownWorkspace", "ws-absent")
	if err != nil {
		t.Fatalf("markerExists: %v", err)
	}
	if exists {
		t.Fatal("chownWorkspace wrote the marker for a workspace it never chowned")
	}
}

func TestChownWorkspaceIgnoresForeignMarkerWithoutWorkspaceID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("markers live under /var/devsy; writing them needs root")
	}
	t.Setenv(pkgconfig.EnvWorkspaceID, "")
	if err := writeMarker("chownWorkspace", "some-other-workspace"); err != nil {
		t.Fatalf("writeMarker: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(pkgconfig.ContainerDataDir, "chownWorkspace.marker"))
	})
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: t.TempDir(),
		},
	}
	if err := chownWorkspace(result, false); err != nil {
		t.Fatalf("chownWorkspace: %v", err)
	}

	if got := logs.FilterMessageSnippet("chown workspace:").Len(); got == 0 {
		t.Error("chownWorkspace skipped chowning because of an unrelated workspace's marker")
	}
}

// mountReadOnly bind-mounts dir onto itself read-only so Lchown inside it fails with EROFS even for root.
func mountReadOnly(t *testing.T, dir string) {
	t.Helper()
	//nolint:gosec // G204: dir is a t.TempDir() path, not external input
	if err := exec.Command("mount", "--bind", dir, dir).Run(); err != nil {
		t.Skipf("bind mount unavailable in this environment: %v", err)
	}
	t.Cleanup(func() {
		//nolint:gosec // G204: dir is a t.TempDir() path, not external input
		_ = exec.Command("umount", dir).Run()
	})
	//nolint:gosec // G204: dir is a t.TempDir() path, not external input
	if err := exec.Command("mount", "-o", "remount,bind,ro", dir).Run(); err != nil {
		t.Fatalf("remount read-only: %v", err)
	}
}

// newForeignOwnedDir creates dir/f.txt owned by a uid other than root, so a
// chown to root actually attempts (and, once read-only, fails) reassignment.
func newForeignOwnedDir(t *testing.T) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), "ws")
	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(folder, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Lchown(folder, 1, 1); err != nil {
		t.Fatalf("chown folder to non-root owner: %v", err)
	}
	if err := os.Lchown(file, 1, 1); err != nil {
		t.Fatalf("chown file to non-root owner: %v", err)
	}
	return folder
}

func TestChownWorkspaceDeniedRecursiveChownSkipsMarker(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: writes markers under /var/devsy and bind-mounts read-only")
	}

	folder := newForeignOwnedDir(t)
	mountReadOnly(t, folder)

	t.Setenv(pkgconfig.EnvWorkspaceID, "ws-denied")
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(pkgconfig.ContainerDataDir, "chownWorkspace.marker"))
	})

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: folder},
	}
	if err := chownWorkspace(result, true); err != nil {
		t.Fatalf("chownWorkspace: %v", err)
	}

	exists, err := markerExists("chownWorkspace", "ws-denied")
	if err != nil {
		t.Fatalf("markerExists: %v", err)
	}
	if exists {
		t.Fatal("chownWorkspace latched the marker despite a fully denied recursive chown")
	}
}

func TestChownWorkspaceDeniedWorkspaceRootChownFails(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: writes markers under /var/devsy and bind-mounts read-only")
	}

	parent := t.TempDir()
	folder := filepath.Join(parent, "ws")
	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mountReadOnly(t, parent)

	t.Setenv(pkgconfig.EnvWorkspaceID, "ws-root-denied")
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(pkgconfig.ContainerDataDir, "chownWorkspace.marker"))
	})

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: folder},
	}
	if err := chownWorkspace(result, false); err == nil {
		t.Fatal("expected chownWorkspace to fail when the workspace-root chown is denied")
	}

	exists, err := markerExists("chownWorkspace", "ws-root-denied")
	if err != nil {
		t.Fatalf("markerExists: %v", err)
	}
	if exists {
		t.Fatal("chownWorkspace latched the marker despite a denied workspace-root chown")
	}
}
