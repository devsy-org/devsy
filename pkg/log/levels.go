package log

import "go.uber.org/zap/zapcore"

const DefaultLevel = "error"

var validLevels = [...]string{"error", "warn", "info", "debug", "trace"}

// LevelFromString parses the user-facing finite log-level set.
func LevelFromString(value string) (zapcore.Level, bool) {
	switch value {
	case "error":
		return zapcore.ErrorLevel, true
	case "warn":
		return zapcore.WarnLevel, true
	case "info":
		return zapcore.InfoLevel, true
	case "debug":
		return zapcore.DebugLevel, true
	case "trace":
		return zapcore.DebugLevel - 1, true
	default:
		return zapcore.ErrorLevel, false
	}
}

func ValidLevels() []string { return append([]string(nil), validLevels[:]...) }

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
	return sugar.Load().Desugar().Core().Enabled(zapcore.DebugLevel)
}

// LevelString returns the lowest enabled log level as a lowercase string
// (e.g. "debug", "info", "warn", "error", "fatal").
func LevelString() string {
	core := sugar.Load().Desugar().Core()
	if core.Enabled(zapcore.DebugLevel - 1) {
		return "trace"
	}
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
