package log

import (
	"bytes"
	"io"
	"os"
	"sync"

	"github.com/devsy-org/devsy/pkg/secrets"
	"go.uber.org/zap/zapcore"
)

// maxPendingLine bounds how much unterminated output levelWriter buffers
// before logging it anyway, so a subprocess that never emits a newline
// can't grow the pending line without limit.
const maxPendingLine = 64 * 1024

func Writer(level int) io.WriteCloser {
	return &levelWriter{level: verbosityConstToZapLevel(level)}
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
	if len(redacted) > 0 {
		_, _ = os.Stderr.Write(redacted)
		_, _ = extraSinks.Write(redacted)
	}
	return len(p), nil
}

func (w *passthroughWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream != nil {
		if pending := w.stream.Flush(); pending != "" {
			_, _ = os.Stderr.Write([]byte(pending))
			_, _ = extraSinks.Write([]byte(pending))
		}
	}
	return nil
}

func verbosityConstToZapLevel(level int) zapcore.Level {
	switch level {
	case LevelDebug:
		return zapcore.DebugLevel
	case LevelInfo: // LevelWarn has the same value
		return zapcore.InfoLevel
	case LevelError:
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

type levelWriter struct {
	level zapcore.Level

	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *levelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !sugar.Load().Desugar().Core().Enabled(w.level) {
		return len(p), nil // discard if below current level
	}

	total := len(p)
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
	return total, nil
}

func (w *levelWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.logLine(w.buf.String())
		w.buf.Reset()
	}
	return nil
}

func (w *levelWriter) logLine(line string) {
	switch w.level {
	case zapcore.DebugLevel:
		Debug(line)
	case zapcore.ErrorLevel:
		Error(line)
	default:
		Info(line)
	}
}
