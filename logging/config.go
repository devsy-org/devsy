package logging

import (
	"log/slog"
	"strings"
)

const (
	LevelTrace slog.Level = -8
	LevelFatal slog.Level = 12
)

const (
	levelTraceName = "trace"
	levelDebugName = "debug"
	levelInfoName  = "info"
	levelWarnName  = "warn"
	levelErrorName = "error"
)

type Format string

const (
	FormatAuto   Format = "auto"
	FormatJSON   Format = "json"
	FormatText   Format = "text"
	FormatLogfmt Format = "logfmt"
)

type ProviderConfig struct {
	Level  slog.Level
	Format Format
}

type InvalidConfig struct {
	Level  bool
	Format bool
}

func ParseLevel(value string) (slog.Level, bool) {
	switch value {
	case levelTraceName:
		return LevelTrace, true
	case levelDebugName:
		return slog.LevelDebug, true
	case levelInfoName:
		return slog.LevelInfo, true
	case levelWarnName:
		return slog.LevelWarn, true
	case levelErrorName:
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

func LevelNames() []string {
	return []string{levelErrorName, levelWarnName, levelInfoName, levelDebugName, levelTraceName}
}

func LevelName(level slog.Level) string {
	switch {
	case level < slog.LevelDebug:
		return levelTraceName
	case level < slog.LevelInfo:
		return levelDebugName
	case level < slog.LevelWarn:
		return levelInfoName
	case level < slog.LevelError:
		return levelWarnName
	case level < LevelFatal:
		return levelErrorName
	default:
		return "fatal"
	}
}

func ParseFormat(value string) (Format, bool) {
	switch Format(value) {
	case FormatAuto, FormatJSON, FormatText, FormatLogfmt:
		return Format(value), true
	case "plain":
		return FormatText, true
	default:
		return "", false
	}
}

func ResolveProvider(level, format string) (ProviderConfig, InvalidConfig) {
	level = strings.ToLower(level)
	format = strings.ToLower(format)
	if level == "" {
		level = levelInfoName
	}
	parsedLevel, levelOK := ParseLevel(level)
	parsedFormat, formatOK := resolveProviderFormat(format)
	return ProviderConfig{Level: parsedLevel, Format: parsedFormat},
		InvalidConfig{Level: !levelOK, Format: !formatOK}
}

func resolveProviderFormat(value string) (Format, bool) {
	if value == "" {
		return FormatJSON, true
	}
	format, ok := ParseFormat(value)
	if !ok {
		return FormatJSON, false
	}
	if format == FormatAuto {
		return FormatJSON, true
	}
	return format, true
}
