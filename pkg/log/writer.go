package log

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/devsy-org/devsy/pkg/secrets"
)

// maxPendingLine bounds how much unterminated output levelWriter buffers
// before logging it anyway, so a subprocess that never emits a newline
// can't grow the pending line without limit.
const maxPendingLine = 64 * 1024

func Writer(level Level) io.WriteCloser {
	return &levelWriter{level: level, stream: secrets.NewStreamingRedactor(current.Load().redactor)}
}

// PassthroughWriter writes bytes exactly as received, with no level
// filtering, line buffering, or structured formatting.
func PassthroughWriter() io.WriteCloser {
	return PassthroughWriterWithRedactor(secrets.NewEnvironmentRedactor(os.Environ()))
}

// PassthroughWriterWithRedactor writes raw bytes to stderr while applying the
// supplied streaming redactor before anything leaves the process.
func PassthroughWriterWithRedactor(redactor *secrets.Redactor) io.WriteCloser {
	return &passthroughWriter{
		stream: secrets.NewStreamingRedactor(redactor),
	}
}

type passthroughWriter struct {
	mu     sync.Mutex
	stream *secrets.StreamingRedactor
}

func (w *passthroughWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	redacted := []byte(w.stream.RedactChunk(string(p)))
	if len(redacted) == 0 {
		return len(p), nil
	}
	return len(p), writePassthrough(redacted)
}

func (w *passthroughWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream != nil {
		if pending := w.stream.Flush(); pending != "" {
			return writePassthrough([]byte(pending))
		}
	}
	return nil
}

func writePassthrough(p []byte) error {
	n, err := os.Stderr.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	_, sinkErr := extraSinks.Write(p)
	return errors.Join(err, sinkErr)
}

type levelWriter struct {
	level  Level
	stream *secrets.StreamingRedactor

	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *levelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !enabled(w.level) {
		return len(p), nil
	}

	total := len(p)
	p = []byte(w.stream.RedactChunk(string(p)))
	w.writeLines(p)
	return total, nil
}

func (w *levelWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeLines([]byte(w.stream.Flush()))
	if w.buf.Len() > 0 {
		w.logLine(w.buf.String())
		w.buf.Reset()
	}
	return nil
}

func (w *levelWriter) writeLines(p []byte) {
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			break
		}
		w.buf.Write(p[:i])
		w.logLine(w.buf.String())
		w.buf.Reset()
		p = p[i+1:]
	}
	w.buf.Write(p)
	if w.buf.Len() > maxPendingLine {
		w.logLine(w.buf.String())
		w.buf.Reset()
	}
}

func (w *levelWriter) logLine(line string) {
	emit(w.level, line)
}
