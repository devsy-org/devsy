package flatpak

import (
	"os"
	"path/filepath"
	"slices"
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

const testHome = "/home/u"

func TestHostSpawnDirectory(t *testing.T) {
	cases := []struct{ name, cwd, home, want string }{
		{"host path kept", "/home/u/project", testHome, "/home/u/project"},
		{"app root falls back to home", "/app", testHome, testHome},
		{"app subdirectory falls back to home", "/app/extra/claude-desktop", testHome, testHome},
		{"app-like prefix kept", "/application/data", testHome, "/application/data"},
		{"empty cwd falls back to home", "", testHome, testHome},
		{"empty cwd and home yields empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostSpawnDirectory(tc.cwd, tc.home); got != tc.want {
				t.Fatalf("hostSpawnDirectory(%q, %q) = %q, want %q", tc.cwd, tc.home, got, tc.want)
			}
		})
	}
}

func TestBuildSpawnArgs(t *testing.T) {
	got := buildSpawnArgs("/h/devsy", "/app/x", testHome, []string{"workspace", "ssh"})
	want := []string{"--directory=/home/u", "--host", "/h/devsy", "workspace", "ssh"}
	if !slices.Equal(got, want) {
		t.Fatalf("buildSpawnArgs() = %v, want %v", got, want)
	}

	got = buildSpawnArgs("/h/devsy", "", "", []string{"up"})
	want = []string{"--host", "/h/devsy", "up"}
	if !slices.Equal(got, want) {
		t.Fatalf("buildSpawnArgs() without directory = %v, want %v", got, want)
	}
}

func withFlatpakInfoPath(t *testing.T, path string) {
	t.Helper()
	orig := flatpakInfoPath
	flatpakInfoPath = path
	t.Cleanup(func() { flatpakInfoPath = orig })
}
