package workspace

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/task"
)

func newTestTailer(t *testing.T, taskID string) (*logTailer, string) {
	t.Helper()
	dir := t.TempDir()
	config.SetPathManager(fakeRuntimeDirPathManager{dir: dir})
	t.Cleanup(config.ResetPathManager)

	tailer := newLogTailer(taskID)
	if tailer.path == "" {
		t.Fatal("newLogTailer produced an empty path")
	}
	return tailer, tailer.path
}

// fakeRuntimeDirPathManager overrides only RuntimeDir so tests can point
// ProcessStreamsFile at a temp directory without touching the real state dir.
type fakeRuntimeDirPathManager struct {
	config.PathManager
	dir string
}

func (f fakeRuntimeDirPathManager) RuntimeDir() (string, error) { return f.dir, nil }

func (f fakeRuntimeDirPathManager) ProcessStreamsFile(name string) (string, error) {
	return filepath.Join(f.dir, name+".streams"), nil
}

func TestLogTailerEmitsLinesAsTheyAreAppended(t *testing.T) {
	tailer, path := newTestTailer(t, "task1")
	var out bytes.Buffer

	tailer.poll(&out) // file doesn't exist yet
	if out.Len() != 0 {
		t.Fatalf("expected no output before the file exists, got %q", out.String())
	}

	if err := os.WriteFile(path, []byte("first line\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tailer.poll(&out)
	if out.String() != "first line\n" {
		t.Fatalf("got %q, want %q", out.String(), "first line\n")
	}

	// #nosec G304 -- path is t.TempDir()-derived
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("second line\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()

	tailer.poll(&out)
	if out.String() != "first line\nsecond line\n" {
		t.Fatalf("got %q, want both lines", out.String())
	}
}

func TestLogTailerSkipsStructuredEnvelopeLines(t *testing.T) {
	tailer, path := newTestTailer(t, "task2")
	content := `{"level":"debug","ts":"2026-01-01T00:00:00.000-0500","msg":"a debug line"}
{"kind":"status","phase":"building_image","started":true}
{"kind":"result","outcome":"success","containerId":"abc"}
plain unstructured line
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	tailer.poll(&out)

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("a debug line")) {
		t.Errorf("expected the debug line to pass through, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("plain unstructured line")) {
		t.Errorf("expected the unstructured line to pass through, got %q", got)
	}
	if bytes.Contains([]byte(got), []byte(`"kind"`)) {
		t.Errorf("expected structured envelope lines to be filtered out, got %q", got)
	}
}

func TestLogTailerFlushEmitsTrailingPartialLine(t *testing.T) {
	tailer, path := newTestTailer(t, "task3")
	// No trailing newline: simulates reading mid-write, or the worker's
	// final line never getting a newline before it exits.
	if err := os.WriteFile(path, []byte("unterminated"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	tailer.poll(&out)
	if out.Len() != 0 {
		t.Fatalf("expected the partial line to be buffered, not emitted yet, got %q", out.String())
	}

	tailer.flush(&out)
	if out.String() != "unterminated\n" {
		t.Fatalf("got %q, want %q", out.String(), "unterminated\n")
	}
}

func TestFollowTaskStreamsWorkerLogOutput(t *testing.T) {
	// End-to-end: followTask must forward the worker's real log output
	// (captured in its streams file) to stderr as it polls, not just the
	// synthesized phase-transition events from task state.
	dir := t.TempDir()
	config.SetPathManager(fakeRuntimeDirPathManager{dir: dir})
	t.Cleanup(config.ResetPathManager)

	store, err := task.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	streamsPath, err := config.DefaultPathManager().
		ProcessStreamsFile(task.WorkerProcessName(tk.ID()))
	if err != nil {
		t.Fatalf("ProcessStreamsFile: %v", err)
	}
	line := `{"level":"debug","ts":"2026-01-01T00:00:00.000-0500","msg":"worker debug line"}` + "\n"
	if err := os.WriteFile(streamsPath, []byte(line), 0o600); err != nil {
		t.Fatalf("write streams file: %v", err)
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = tk.Succeed(nil)
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	followErr := followTask(context.Background(), store, followTaskOptions{
		id:       tk.ID(),
		interval: 10 * time.Millisecond,
		emitJSON: true,
	})
	os.Stderr = origStderr
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if followErr != nil {
		t.Fatalf("followTask: %v", followErr)
	}
	if !strings.Contains(buf.String(), "worker debug line") {
		t.Errorf("expected the worker's debug line on stderr, got %q", buf.String())
	}
}

func TestLogTailerToleratesMissingRuntimeDir(t *testing.T) {
	config.SetPathManager(
		fakeRuntimeDirPathManager{dir: filepath.Join(t.TempDir(), "does-not-exist")},
	)
	t.Cleanup(config.ResetPathManager)

	tailer := newLogTailer("task4")
	var out bytes.Buffer
	tailer.poll(&out)
	tailer.flush(&out)
	if out.Len() != 0 {
		t.Errorf("expected no output when the streams file never appears, got %q", out.String())
	}
}
