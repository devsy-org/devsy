package log

import "log/slog"

type Level = slog.Level

const (
	SlogLevelTrace Level = -8
	SlogLevelDebug Level = -4
	SlogLevelInfo  Level = 0
	SlogLevelWarn  Level = 4
	SlogLevelError Level = 8
	SlogLevelFatal Level = 12
)

const DefaultLevel = LevelWarnName

const (
	LevelErrorName = "error"
	LevelWarnName  = "warn"
	LevelInfoName  = "info"
	LevelDebugName = "debug"
	LevelTraceName = "trace"
)

var validLevels = [...]string{
	LevelErrorName,
	LevelWarnName,
	LevelInfoName,
	LevelDebugName,
	LevelTraceName,
}

func LevelFromString(value string) (Level, bool) {
	switch value {
	case LevelErrorName:
		return SlogLevelError, true
	case LevelWarnName:
		return SlogLevelWarn, true
	case LevelInfoName:
		return SlogLevelInfo, true
	case LevelDebugName:
		return SlogLevelDebug, true
	case LevelTraceName:
		return SlogLevelTrace, true
	default:
		return SlogLevelError, false
	}
}

func ValidLevels() []string { return append([]string(nil), validLevels[:]...) }

func levelName(level Level) string {
	switch {
	case level >= SlogLevelFatal:
		return "fatal"
	case level >= SlogLevelError:
		return LevelErrorName
	case level >= SlogLevelWarn:
		return LevelWarnName
	case level >= SlogLevelInfo:
		return LevelInfoName
	case level >= SlogLevelDebug:
		return LevelDebugName
	default:
		return LevelTraceName
	}
}

func TraceEnabled() bool  { return enabled(SlogLevelTrace) }
func DebugEnabled() bool  { return enabled(SlogLevelDebug) }
func InfoEnabled() bool   { return enabled(SlogLevelInfo) }
func LevelString() string { return levelName(current.Load().level) }
