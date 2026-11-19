package telemetry

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMetricsExportPermissionsAndLockTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics")
	if err := os.WriteFile(path, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVSY_METRICS_FILE", path)
	if err := ExportMetrics(MetricsSnapshot{}); err == nil {
		t.Fatal("accepted unsafe permissions")
	}
	assertMetricsLockTimeout(t, path)
}

func assertMetricsLockTimeout(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	start := time.Now()
	if err := ExportMetrics(MetricsSnapshot{}); err == nil {
		t.Fatal("accepted locked file")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("lock wait was not bounded")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "existing\n" {
		t.Fatalf("existing content changed: %q %v", data, err)
	}
}
