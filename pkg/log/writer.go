package log

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"go.uber.org/zap/zapcore"
)

// Writer returns an io.WriteCloser that logs each complete line written to
// it as a structured entry at the given level, through the same encoder as
// every other log call. Primarily used as a subprocess's Stdout/Stderr.
// Close flushes any trailing line left without a newline.
func Writer(level int) io.WriteCloser {
	return &levelWriter{level: verbosityConstToZapLevel(level)}
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

	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// Incomplete line: put it back for the next Write/Close.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		w.logLine(strings.TrimSuffix(line, "\n"))
	}
	return len(p), nil
}

func (w *levelWriter) logLine(line string) {
	if line == "" {
		return
	}
	switch w.level {
	case zapcore.DebugLevel:
		Debug(line)
	case zapcore.ErrorLevel:
		Error(line)
	default:
		Info(line)
	}
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
