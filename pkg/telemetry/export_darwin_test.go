package telemetry

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMetricsExportPermissionsAndLockTimeout(t *testing.T) {
	const filename = "metrics"
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile(filename, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Chmod(filename, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVSY_METRICS_FILE", filepath.Join(root.Name(), filename))
	if err := ExportMetrics(MetricsSnapshot{}); err == nil {
		t.Fatal("accepted unsafe permissions")
	}
	assertMetricsLockTimeout(t, root, filename)
}

func assertMetricsLockTimeout(t *testing.T, root *os.Root, filename string) {
	t.Helper()
	if err := root.Chmod(filename, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := root.OpenFile(filename, os.O_WRONLY, 0)
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
	data, err := root.ReadFile(filename)
	if err != nil || string(data) != "existing\n" {
		t.Fatalf("existing content changed: %q %v", data, err)
	}
}
