package flatpak

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInSandbox(t *testing.T) {
	t.Run("false when marker absent", func(t *testing.T) {
		withFlatpakInfoPath(t, filepath.Join(t.TempDir(), "does-not-exist"))
		if InSandbox() {
			t.Fatal("expected InSandbox to be false when marker file is absent")
		}
	})

	t.Run("true when marker present", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), ".flatpak-info")
		if err := os.WriteFile(marker, []byte("[Application]\n"), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		withFlatpakInfoPath(t, marker)
		if !InSandbox() {
			t.Fatal("expected InSandbox to be true when marker file exists")
		}
	})
}

func TestHostBinaryPath(t *testing.T) {
	t.Run("honors XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/custom/data")
		if got, want := HostBinaryPath(), "/custom/data/devsy/devsy"; got != want {
			t.Fatalf("HostBinaryPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to HOME/.local/share", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/tester")
		if got, want := HostBinaryPath(), "/home/tester/.local/share/devsy/devsy"; got != want {
			t.Fatalf("HostBinaryPath() = %q, want %q", got, want)
		}
	})
}

func TestReexecOnHost_NoopOutsideSandbox(t *testing.T) {
	withFlatpakInfoPath(t, filepath.Join(t.TempDir(), "does-not-exist"))

	shouldExit, err := ReexecOnHost()
	if err != nil {
		t.Fatalf("ReexecOnHost() error = %v, want nil", err)
	}
	if shouldExit {
		t.Fatal("ReexecOnHost() shouldExit = true outside sandbox, want false")
	}
}

func TestReexecOnHost_MissingHostBinary(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".flatpak-info")
	if err := os.WriteFile(marker, []byte("[Application]\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	withFlatpakInfoPath(t, marker)
	t.Setenv("XDG_DATA_HOME", dir)

	shouldExit, err := ReexecOnHost()
	if err == nil {
		t.Fatal("ReexecOnHost() error = nil, want missing-binary error")
	}
	if shouldExit {
		t.Fatal("ReexecOnHost() shouldExit = true on pre-check failure, want false")
	}
}

func withFlatpakInfoPath(t *testing.T, path string) {
	t.Helper()
	orig := flatpakInfoPath
	flatpakInfoPath = path
	t.Cleanup(func() { flatpakInfoPath = orig })
}
