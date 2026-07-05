//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const testOSLinux = "linux"

func newTestLinuxPM() PathManager {
	pm := &linuxPathManager{}
	pm.pm = pm

	return pm
}

func skipIfNotLinux(t *testing.T) {
	t.Helper()

	if runtime.GOOS != testOSLinux {
		t.Skip("linux-only test")
	}
}

func TestLinuxDefaults(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestLinuxPM()
	uid := os.Getuid()
	wantRuntime := filepath.Join(
		os.TempDir(),
		fmt.Sprintf("%s-%d", RepoName, uid),
	)

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"ConfigDir", pm.ConfigDir, filepath.Join(home, ".config", RepoName)},
		{"DataDir", pm.DataDir, filepath.Join(home, "."+RepoName)},
		{"CacheDir", pm.CacheDir, filepath.Join(home, ".cache", RepoName)},
		{"StateDir", pm.StateDir, filepath.Join(home, "."+RepoName, "state")},
		{"RuntimeDir", pm.RuntimeDir, wantRuntime},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigFilePathDefault(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")

	pm := newTestLinuxPM()

	got, err := pm.ConfigFilePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(home, ".config", RepoName, ConfigFile)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigFilePathMigratesLegacyConfig(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")

	pm := newTestLinuxPM()

	dataDir, err := pm.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	legacyPath := filepath.Join(dataDir, ConfigFile)
	want := []byte("defaultContext: default\n")
	if err := os.WriteFile(legacyPath, want, 0o600); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}

	got, err := pm.ConfigFilePath()
	if err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}

	newPath := filepath.Join(home, ".config", RepoName, ConfigFile)
	if got != newPath {
		t.Errorf("ConfigFilePath = %q, want %q", got, newPath)
	}

	migrated, err := os.ReadFile(newPath) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if string(migrated) != string(want) {
		t.Errorf("migrated contents = %q, want %q", migrated, want)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("legacy config still present at %q (err=%v)", legacyPath, err)
	}
}

func TestConfigFilePathKeepsExistingConfig(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")

	pm := newTestLinuxPM()

	dataDir, err := pm.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	legacyPath := filepath.Join(dataDir, ConfigFile)
	if err := os.WriteFile(legacyPath, []byte("legacy\n"), 0o600); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}

	newPath := filepath.Join(home, ".config", RepoName, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	want := []byte("current\n")
	if err := os.WriteFile(newPath, want, 0o600); err != nil {
		t.Fatalf("seed current config: %v", err)
	}

	if _, err := pm.ConfigFilePath(); err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}

	got, err := os.ReadFile(newPath) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("config was overwritten: got %q, want %q", got, want)
	}
}

func TestConfigFilePathDEVSYCONFIGOverride(t *testing.T) {
	custom := "/custom/path/config.yaml"
	t.Setenv(EnvConfig, custom)

	pm := newTestLinuxPM()

	got, err := pm.ConfigFilePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != custom {
		t.Errorf("got %q, want %q", got, custom)
	}
}

func TestContextDataSubPaths(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestLinuxPM()
	ctx := "myctx"
	base := filepath.Join(home, "."+RepoName, "contexts", ctx)

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"ContextDir", func() (string, error) { return pm.ContextDir(ctx) }, base},
		{
			"WorkspacesDir",
			func() (string, error) { return pm.WorkspacesDir(ctx) },
			filepath.Join(base, "workspaces"),
		},
		{
			"WorkspaceDir",
			func() (string, error) { return pm.WorkspaceDir(ctx, "ws1") },
			filepath.Join(base, "workspaces", "ws1"),
		},
		{
			"WorkspaceAgentDir",
			func() (string, error) { return pm.WorkspaceAgentDir(ctx, "ws1") },
			filepath.Join(base, "workspaces", "ws1", "agent"),
		},
		{
			"WorkspaceContentsDir",
			func() (string, error) { return pm.WorkspaceContentsDir(ctx) },
			filepath.Join(base, "contents"),
		},
		{
			"WorkspaceContentDir",
			func() (string, error) { return pm.WorkspaceContentDir(ctx, "ws1") },
			filepath.Join(base, "contents", "ws1"),
		},
		{
			"WorkspaceLogDir",
			func() (string, error) { return pm.WorkspaceLogDir(ctx, "ws1") },
			filepath.Join(base, "workspaces", "ws1", "logs"),
		},
		{
			"MachinesDir",
			func() (string, error) { return pm.MachinesDir(ctx) },
			filepath.Join(base, "machines"),
		},
		{
			"MachineDir",
			func() (string, error) { return pm.MachineDir(ctx, "m1") },
			filepath.Join(base, "machines", "m1"),
		},
		{
			"ProvidersDir",
			func() (string, error) { return pm.ProvidersDir(ctx) },
			filepath.Join(base, "providers"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderAndProInstanceSubPaths(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestLinuxPM()
	ctx := "myctx"
	base := filepath.Join(home, "."+RepoName, "contexts", ctx)

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{
			"ProviderDir",
			func() (string, error) { return pm.ProviderDir(ctx, "docker") },
			filepath.Join(base, "providers", "docker"),
		},
		{
			"ProviderBinariesDir",
			func() (string, error) { return pm.ProviderBinariesDir(ctx, "docker") },
			filepath.Join(base, "providers", "docker", "binaries"),
		},
		{
			"ProviderDaemonDir",
			func() (string, error) { return pm.ProviderDaemonDir(ctx, "docker") },
			filepath.Join(base, "providers", "docker", "daemon"),
		},
		{
			"ProInstancesDir",
			func() (string, error) { return pm.ProInstancesDir(ctx) },
			filepath.Join(base, "pro_instances"),
		},
		{
			"ProInstanceDir",
			func() (string, error) { return pm.ProInstanceDir(ctx, "pro.example.com") },
			filepath.Join(base, "pro_instances", "pro.example.com"),
		},
		{
			"LocksDir",
			func() (string, error) { return pm.LocksDir(ctx) },
			filepath.Join(base, "locks"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCacheSubPaths(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestLinuxPM()
	base := filepath.Join(home, ".cache", RepoName)

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"AgentCacheDir", pm.AgentCacheDir, filepath.Join(base, "agents")},
		{"ProviderDownloadCacheDir", pm.ProviderDownloadCacheDir, filepath.Join(base, "providers")},
		{
			"FeatureCacheDir",
			func() (string, error) { return pm.FeatureCacheDir("abc123") },
			filepath.Join(base, "features", "abc123"),
		},
		{"PlatformCacheDir", pm.PlatformCacheDir, filepath.Join(base, "platform")},
		{"SSHKeysDir", pm.SSHKeysDir, filepath.Join(base, "keys")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRuntimeSubPaths(t *testing.T) {
	skipIfNotLinux(t)

	pm := newTestLinuxPM()
	uid := os.Getuid()
	base := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, uid))

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"DaemonPIDFile", pm.DaemonPIDFile, filepath.Join(base, DaemonProcessName+".pid")},
		{"DaemonLockFile", pm.DaemonLockFile, filepath.Join(base, DaemonProcessName+".lock")},
		{
			"DaemonStreamsFile",
			pm.DaemonStreamsFile,
			filepath.Join(base, DaemonProcessName+".streams"),
		},
		{
			"ProcessPIDFile",
			func() (string, error) { return pm.ProcessPIDFile("myproc") },
			filepath.Join(base, "myproc.pid"),
		},
		{
			"ProcessLockFile",
			func() (string, error) { return pm.ProcessLockFile("myproc") },
			filepath.Join(base, "myproc.lock"),
		},
		{
			"ProcessStreamsFile",
			func() (string, error) { return pm.ProcessStreamsFile("myproc") },
			filepath.Join(base, "myproc.streams"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStateSubPaths(t *testing.T) {
	skipIfNotLinux(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestLinuxPM()

	got, err := pm.LogDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(home, "."+RepoName, "state", "logs")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
