package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteFileAtomic_ConcurrentReadersSeeNoPartialWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.json")

	// Seed with valid JSON so the first reads succeed.
	if err := WriteFileAtomic(path, []byte(`{"n":0}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	readerErr := make(chan error, 4)

	wg.Go(func() { atomicWriter(t, &stop, path) })
	for range 3 {
		wg.Go(func() { atomicReader(&stop, path, readerErr) })
	}

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	close(readerErr)

	for err := range readerErr {
		t.Fatalf("reader observed partial write: %v", err)
	}
}

func atomicWriter(t *testing.T, stop *atomic.Bool, path string) {
	t.Helper()
	for i := 0; !stop.Load(); i++ {
		payload, _ := json.Marshal(map[string]int{"n": i})
		if err := WriteFileAtomic(path, payload, 0o600); err != nil {
			t.Errorf("write: %v", err)
			return
		}
	}
}

func atomicReader(stop *atomic.Bool, path string, out chan<- error) {
	for !stop.Load() {
		data, err := os.ReadFile(path) //nolint:gosec // test reads a path under t.TempDir
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			out <- err
			return
		}
		var v map[string]int
		if err := json.Unmarshal(data, &v); err != nil {
			out <- err
			return
		}
	}
}

func TestWriteFileAtomic_SucceedsAndDataIsDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "durable.json")

	if err := WriteFileAtomic(path, []byte(`{"durable":true}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test reads a path under t.TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != `{"durable":true}` {
		t.Fatalf("got %q, want the written content", data)
	}
}
