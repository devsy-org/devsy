package log

import (
	"io"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Writer returns an io.WriteCloser that writes each line as a log entry at the given level.
// Level uses the package constants: LevelInfo, LevelDebug, etc.
func Writer(level int) io.WriteCloser {
	zapLevel := verbosityConstToZapLevel(level)
	w, closer, _ := zap.Open("stderr")
	_ = closer // stderr doesn't need closing
	return &levelWriter{
		sink:  w,
		level: zapLevel,
		core:  sugar.Load().Desugar().Core(),
	}
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
	sink  zapcore.WriteSyncer
	level zapcore.Level
	core  zapcore.Core
}

func (w *levelWriter) Write(p []byte) (int, error) {
	if !w.core.Enabled(w.level) {
		return len(p), nil // discard if below current level
	}
	return w.sink.Write(p)
}

func (w *levelWriter) Close() error {
	return nil
}
