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

func TestMetricsExportConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestMetricsExportProcess$")
			cmd.Env = append(os.Environ(), "DEVSY_TEST_METRICS_CHILD=1", "DEVSY_METRICS_FILE="+path)
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
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
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
