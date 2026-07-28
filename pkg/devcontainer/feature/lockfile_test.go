package feature

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	lockTestFeatureA  = "ghcr.io/a/feature:1"
	lockTestVersion   = "1.0.0"
	lockTestResolvedA = "ghcr.io/a/feature@sha256:a"
	lockTestShaA      = "sha256:a"
	lockTestShaB      = "sha256:b"
)

func TestLockfilePath(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   string
	}{
		{
			name:   "standard config",
			origin: "/repo/.devcontainer/devcontainer.json",
			want:   "/repo/.devcontainer/devcontainer-lock.json",
		},
		{
			name:   "hidden config at root",
			origin: "/repo/.devcontainer.json",
			want:   "/repo/.devcontainer-lock.json",
		},
		{
			name:   "empty origin",
			origin: "",
			want:   "",
		},
		{
			name:   "non-local origin",
			origin: "oci://ghcr.io/org/config",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LockfilePath(tt.origin); got != tt.want {
				t.Errorf("LockfilePath(%q) = %q, want %q", tt.origin, got, tt.want)
			}
		})
	}
}

func TestReadLockfile_MissingReturnsEmpty(t *testing.T) {
	lf, err := ReadLockfile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("ReadLockfile: %v", err)
	}
	if lf == nil || lf.Features == nil || len(lf.Features) != 0 {
		t.Fatalf("expected empty lockfile, got %+v", lf)
	}
}

func TestReadLockfile_ParsesEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	content := `{
  "features": {
    "ghcr.io/devcontainers/features/git:1": {
      "version": "1.3.8",
      "resolved": "ghcr.io/devcontainers/features/git@sha256:abc",
      "integrity": "sha256:abc"
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	lf, err := ReadLockfile(path)
	if err != nil {
		t.Fatalf("ReadLockfile: %v", err)
	}
	entry, ok := lf.Features["ghcr.io/devcontainers/features/git:1"]
	if !ok {
		t.Fatal("expected git feature entry")
	}
	if entry.Version != "1.3.8" || entry.Integrity != "sha256:abc" {
		t.Errorf("unexpected entry: %+v", entry)
	}
}

func TestReadLockfile_NormalizesLegacyObjectDependsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	// Legacy/pre-fix lockfiles stored dependsOn as an object mapping IDs to
	// option objects. ReadLockfile must accept it and keep pinning working.
	content := `{
  "features": {
    "ghcr.io/x/go-task:1": {
      "version": "1.0.0",
      "resolved": "ghcr.io/x/go-task@sha256:t",
      "integrity": "sha256:t",
      "dependsOn": {
        "ghcr.io/x/picolayer:1": {}
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	lf, err := ReadLockfile(path)
	if err != nil {
		t.Fatalf("ReadLockfile: %v", err)
	}
	entry := lf.Features["ghcr.io/x/go-task:1"]
	if entry.Integrity != "sha256:t" {
		t.Errorf("integrity lost: %+v", entry)
	}
	want := []string{"ghcr.io/x/picolayer:1"}
	if !reflect.DeepEqual(entry.DependsOn, want) {
		t.Errorf("dependsOn = %v, want %v", entry.DependsOn, want)
	}
}

func TestReadLockfile_KeepsArrayDependsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	content := `{
  "features": {
    "ghcr.io/x/go-task:1": {
      "version": "1.0.0",
      "integrity": "sha256:t",
      "dependsOn": ["ghcr.io/x/picolayer:1"]
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	lf, err := ReadLockfile(path)
	if err != nil {
		t.Fatalf("ReadLockfile: %v", err)
	}
	want := []string{"ghcr.io/x/picolayer:1"}
	if got := lf.Features["ghcr.io/x/go-task:1"].DependsOn; !reflect.DeepEqual(got, want) {
		t.Errorf("dependsOn = %v, want %v", got, want)
	}
}

func TestWriteLockfile_CreatesSortedStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	lf := &Lockfile{Features: map[string]LockedFeature{
		"ghcr.io/b/feature:1": {
			Version:   "2.0.0",
			Resolved:  "ghcr.io/b/feature@sha256:b",
			Integrity: lockTestShaB,
		},
		lockTestFeatureA: {
			Version:   lockTestVersion,
			Resolved:  lockTestResolvedA,
			Integrity: lockTestShaA,
		},
	}}

	if err := WriteLockfile(path, lf, false); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasSuffix(content, "}\n") {
		t.Errorf("expected trailing newline, got %q", content[len(content)-3:])
	}
	// Keys must be emitted in sorted order.
	if strings.Index(content, "ghcr.io/a/feature") > strings.Index(content, "ghcr.io/b/feature") {
		t.Errorf("expected sorted keys, got:\n%s", content)
	}
}

func TestWriteLockfile_DependsOnIsArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	lf := &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {
			Version:   lockTestVersion,
			Resolved:  lockTestResolvedA,
			Integrity: lockTestShaA,
			DependsOn: []string{"ghcr.io/b/feature:1"},
		},
	}}

	if err := WriteLockfile(path, lf, false); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// The reference devcontainer CLI serializes dependsOn as an array of
	// feature identifiers, not an object.
	if !strings.Contains(content, "\"dependsOn\": [") {
		t.Errorf("expected dependsOn as JSON array, got:\n%s", content)
	}
}

func TestWriteLockfile_SkipsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	lf := &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {Version: lockTestVersion, Integrity: lockTestShaA},
	}}
	if err := WriteLockfile(path, lf, false); err != nil {
		t.Fatal(err)
	}
	info1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Rewrite identical content; file must not be modified.
	if err := WriteLockfile(path, lf, false); err != nil {
		t.Fatal(err)
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("expected unchanged lockfile to be left untouched")
	}
}

func TestWriteLockfile_FrozenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	lf := &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {Integrity: lockTestShaA},
	}}
	err := WriteLockfile(path, lf, true)
	if err == nil {
		t.Fatal("expected error for frozen missing lockfile")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("frozen mode must not create the lockfile")
	}
}

func TestWriteLockfile_FrozenMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	existing := &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {Integrity: "sha256:old"},
	}}
	if err := WriteLockfile(path, existing, false); err != nil {
		t.Fatal(err)
	}

	updated := &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {Integrity: "sha256:new"},
	}}
	if err := WriteLockfile(path, updated, true); err == nil {
		t.Fatal("expected error for frozen mismatched lockfile")
	}

	// Original content must be preserved.
	lf, err := ReadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Features[lockTestFeatureA].Integrity != "sha256:old" {
		t.Error("frozen mismatch must not overwrite the lockfile")
	}
}

func TestWriteLockfile_FrozenMatchSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer-lock.json")
	lf := &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {Integrity: lockTestShaA},
	}}
	if err := WriteLockfile(path, lf, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(path, lf, true); err != nil {
		t.Errorf("frozen matching lockfile should succeed, got %v", err)
	}
}

func TestLockfileState_PinAndRecord(t *testing.T) {
	state := &lockfileState{
		loaded: &Lockfile{Features: map[string]LockedFeature{
			lockTestFeatureA: {
				Resolved:  lockTestResolvedA,
				Integrity: lockTestShaA,
			},
		}},
		entries: map[string]LockedFeature{},
	}

	resolved, integrity, ok := state.pin(lockTestFeatureA)
	if !ok || resolved != lockTestResolvedA || integrity != lockTestShaA {
		t.Errorf("pin returned %q %q %v", resolved, integrity, ok)
	}

	if _, _, ok := state.pin("ghcr.io/missing:1"); ok {
		t.Error("expected no pin for missing feature")
	}

	state.record("ghcr.io/b/feature:1", LockedFeature{Integrity: lockTestShaB})
	if state.entries["ghcr.io/b/feature:1"].Integrity != lockTestShaB {
		t.Error("record did not store entry")
	}
}

func TestLockfileState_NilSafe(t *testing.T) {
	var state *lockfileState
	if _, _, ok := state.pin("x"); ok {
		t.Error("nil state must not pin")
	}
	state.record("x", LockedFeature{}) // must not panic
}

func TestLockfileState_CommitSkipsEmptyWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevContainerConfig{}
	cfg.Origin = filepath.Join(dir, "devcontainer.json")

	state := &lockfileState{entries: map[string]LockedFeature{}}
	if err := state.commit(cfg, lockfileMode{write: true}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := os.Stat(LockfilePath(cfg.Origin)); err == nil {
		t.Error("commit must not create an empty lockfile when none exists")
	}
}

func TestLockfileState_CommitWritesEntries(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevContainerConfig{}
	cfg.Origin = filepath.Join(dir, "devcontainer.json")

	state := &lockfileState{entries: map[string]LockedFeature{
		lockTestFeatureA: {Version: lockTestVersion, Integrity: lockTestShaA},
	}}
	if err := state.commit(cfg, lockfileMode{write: true}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	lf, err := ReadLockfile(LockfilePath(cfg.Origin))
	if err != nil {
		t.Fatal(err)
	}
	if lf.Features[lockTestFeatureA].Version != lockTestVersion {
		t.Errorf("expected written entry, got %+v", lf.Features)
	}
}

func TestLockfileState_CommitReadOnlySkipsWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevContainerConfig{}
	cfg.Origin = filepath.Join(dir, "devcontainer.json")

	state := &lockfileState{entries: map[string]LockedFeature{
		lockTestFeatureA: {Integrity: lockTestShaA},
	}}
	if err := state.commit(cfg, lockfileMode{}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := os.Stat(LockfilePath(cfg.Origin)); err == nil {
		t.Error("read-only commit must not write the lockfile")
	}
}

func TestLockfileState_CommitDisabledIsNoOp(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevContainerConfig{}
	cfg.Origin = filepath.Join(dir, "devcontainer.json")

	// --no-lockfile yields a nil state; commit must never write.
	var state *lockfileState
	if err := state.commit(cfg, lockfileMode{write: true, disabled: true}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := os.Stat(LockfilePath(cfg.Origin)); err == nil {
		t.Error("disabled lockfile must not write a file")
	}
}

func TestLockfileState_CheckFrozenPrecondition(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevContainerConfig{}
	cfg.Features = map[string]any{lockTestFeatureA: map[string]any{}}
	cfg.Origin = filepath.Join(dir, "devcontainer.json")

	state := &lockfileState{entries: map[string]LockedFeature{}}

	// Features declared but no lockfile on disk -> fail fast.
	if err := state.checkFrozenPrecondition(cfg); err == nil {
		t.Error("expected frozen precondition to fail when lockfile missing")
	}

	// No declared features -> nothing to enforce.
	empty := &config.DevContainerConfig{}
	empty.Origin = cfg.Origin
	if err := state.checkFrozenPrecondition(empty); err != nil {
		t.Errorf("expected no error with no features, got %v", err)
	}

	// Lockfile present -> precondition passes (mismatch enforced later).
	if err := WriteLockfile(LockfilePath(cfg.Origin), &Lockfile{Features: map[string]LockedFeature{
		lockTestFeatureA: {Integrity: lockTestShaA},
	}}, false); err != nil {
		t.Fatal(err)
	}
	if err := state.checkFrozenPrecondition(cfg); err != nil {
		t.Errorf("expected precondition to pass with lockfile present, got %v", err)
	}
}

func TestVerifyPinnedDigest(t *testing.T) {
	if err := verifyPinnedDigest("id", lockTestShaA, ""); err != nil {
		t.Errorf("no pin should pass: %v", err)
	}
	if err := verifyPinnedDigest("id", lockTestShaA, lockTestShaA); err != nil {
		t.Errorf("matching pin should pass: %v", err)
	}
	if err := verifyPinnedDigest("id", lockTestShaA, lockTestShaB); err == nil {
		t.Error("mismatched pin should fail")
	}
}
