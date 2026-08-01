package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/task"
)

// logTailer streams a detached worker's captured stdout/stderr (see
// pkg/command.StartBackground) as it's written, so `task logs --follow`
// shows the worker's real log output — including debug-level lines —
// instead of only the synthesized phase-transition events.
type logTailer struct {
	path string
	file *os.File
	buf  []byte
}

func newLogTailer(taskID string) *logTailer {
	path, err := config.DefaultPathManager().ProcessStreamsFile(task.WorkerProcessName(taskID))
	if err != nil {
		return &logTailer{}
	}
	return &logTailer{path: path}
}

// poll emits any output appended since the last call. Safe to call before
// the worker has created its streams file; it just retries next time.
func (t *logTailer) poll(w io.Writer) {
	if t.path == "" {
		return
	}
	if t.file == nil {
		f, err := os.Open(t.path) // #nosec G304 -- path derived from our own task ID
		if err != nil {
			return
		}
		t.file = f
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := t.file.Read(buf)
		if n > 0 {
			t.emit(w, buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// flush polls one last time and emits any trailing partial line, then
// releases the file handle. Call once the task has reached a terminal state.
func (t *logTailer) flush(w io.Writer) {
	t.poll(w)
	if len(t.buf) > 0 {
		t.writeLine(w, t.buf)
		t.buf = nil
	}
	if t.file != nil {
		_ = t.file.Close()
		t.file = nil
	}
}

func (t *logTailer) emit(w io.Writer, chunk []byte) {
	t.buf = append(t.buf, chunk...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			return
		}
		line := t.buf[:i]
		t.buf = t.buf[i+1:]
		t.writeLine(w, line)
	}
}

// writeLine skips structured NDJSON envelopes (status/result/error/task):
// the worker's own stdout carries the same envelopes this command already
// reports from polled task state, so passing them through here would just
// duplicate them as noise alongside the worker's actual log lines.
func (t *logTailer) writeLine(w io.Writer, line []byte) {
	if isEnvelopeLine(line) {
		return
	}
	_, _ = w.Write(line)
	_, _ = w.Write([]byte("\n"))
}

func isEnvelopeLine(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return false
	}
	return probe.Kind != ""
}
