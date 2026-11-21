package log

import (
	"log/slog"

	"github.com/devsy-org/devsy/logging"
)

type Level = slog.Level

const DefaultLevel = LevelWarnName

const (
	LevelErrorName = "error"
	LevelWarnName  = "warn"
	LevelInfoName  = "info"
	LevelDebugName = "debug"
	LevelTraceName = "trace"
)

func TraceEnabled() bool  { return enabled(logging.LevelTrace) }
func DebugEnabled() bool  { return enabled(slog.LevelDebug) }
func InfoEnabled() bool   { return enabled(slog.LevelInfo) }
func LevelString() string { return logging.LevelName(current.Load().level) }

const (
	LevelTrace = 3
	LevelDebug = 2
	LevelInfo  = 1
	LevelWarn  = 1
	LevelError = 0
	LevelFatal = 0
)

func LevelFromString(value string) (Level, bool) {
	switch value {
	case LevelErrorName, LevelWarnName, LevelInfoName, LevelDebugName, LevelTraceName:
		return logging.ParseLevel(value)
	default:
		return slog.LevelError, false
	}
}

func ValidLevels() []string {
	return []string{LevelErrorName, LevelWarnName, LevelInfoName, LevelDebugName, LevelTraceName}
}

func VerbosityToLevel(verbosity int) Level {
	switch {
	case verbosity >= 3:
		return logging.LevelTrace
	case verbosity >= 2:
		return slog.LevelDebug
	case verbosity >= 1:
		return slog.LevelInfo
	default:
		return slog.LevelError
	}
}
