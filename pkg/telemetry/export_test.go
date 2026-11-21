package telemetry

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMetricsExportProcess(t *testing.T) {
	if os.Getenv("DEVSY_TEST_METRICS_CHILD") != "1" {
		return
	}
	for i := range 10 {
		if err := ExportMetrics(MetricsSnapshot{DurationMs: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
}

const metricsTestFilename = "metrics.jsonl"

func TestMetricsExportConcurrentProcesses(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	path := filepath.Join(root.Name(), metricsTestFilename)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("test executable path must be absolute")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			cmd := &exec.Cmd{
				Path: executable,
				Args: []string{executable, "-test.run=^TestMetricsExportProcess$"},
				Env:  []string{"DEVSY_TEST_METRICS_CHILD=1", "DEVSY_METRICS_FILE=" + path},
			}
			errs <- cmd.Run()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := root.ReadFile(metricsTestFilename)
	if err != nil {
		t.Fatal(err)
	}
	assertConcurrentMetricsRecords(t, data)
}

func assertConcurrentMetricsRecords(t *testing.T, data []byte) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 40 {
		t.Fatalf("got %d records", len(lines))
	}
	for _, line := range lines {
		var snapshot MetricsSnapshot
		if err := json.Unmarshal([]byte(line), &snapshot); err != nil {
			t.Fatalf("corrupted record: %v", err)
		}
	}
}
