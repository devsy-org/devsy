package log

import "go.uber.org/zap/zapcore"

// Verbosity levels mapped from CLI flags.
const (
	LevelTrace = 3
	LevelDebug = 2
	LevelInfo  = 1
	LevelWarn  = 1
	LevelError = 0
	LevelFatal = 0
)

// DebugEnabled reports whether debug-level messages are currently logged.
func DebugEnabled() bool {
	return sugar.Desugar().Core().Enabled(zapcore.DebugLevel)
}

// LevelString returns the lowest enabled log level as a lowercase string
// (e.g. "debug", "info", "warn", "error", "fatal").
func LevelString() string {
	core := sugar.Desugar().Core()
	for _, l := range []zapcore.Level{
		zapcore.DebugLevel,
		zapcore.InfoLevel,
		zapcore.WarnLevel,
		zapcore.ErrorLevel,
		zapcore.FatalLevel,
	} {
		if core.Enabled(l) {
			return l.String()
		}
	}
	return "info"
}

// VerbosityToLevel converts a -v count (0-3) to a zapcore.Level.
// 0 = error+fatal only, 1 = +warn+info, 2 = +debug, 3 = trace (mapped to zap Debug-1).
func VerbosityToLevel(verbosity int) zapcore.Level {
	switch {
	case verbosity >= 3:
		// Trace: use a custom level below Debug
		return zapcore.DebugLevel - 1
	case verbosity >= 2:
		return zapcore.DebugLevel
	case verbosity >= 1:
		return zapcore.InfoLevel
	default:
		return zapcore.ErrorLevel
	}
}
