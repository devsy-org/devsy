package log

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// errorSummaryMaxRunes bounds an operational log summary, including the
	// truncation marker.
	errorSummaryMaxRunes        = 2048
	truncatedErrorSummarySuffix = "…[truncated]"
)

// LogSummaryProvider is implemented by errors whose Error text carries data
// that must not be replayed into an application log record (for example a
// captured diagnostic tail that was already emitted separately).
type LogSummaryProvider interface {
	LogSummary() string
}

// ErrorForLog returns a bounded, log-safe description of err. The first
// LogSummaryProvider found while unwrapping err supplies the summary;
// otherwise err.Error() is used. The result is capped at 2,048 runes
// including the truncation marker. It never mutates err and never trims
// whitespace: embedded line breaks are escaped later by the text encoder.
func ErrorForLog(err error) string {
	if err == nil {
		return "<nil>"
	}
	summary := err.Error()
	var provider LogSummaryProvider
	if errors.As(err, &provider) {
		summary = provider.LogSummary()
	}
	return truncateErrorSummary(summary)
}

func truncateErrorSummary(summary string) string {
	summary = strings.ToValidUTF8(summary, "\uFFFD")
	if utf8.RuneCountInString(summary) <= errorSummaryMaxRunes {
		return summary
	}
	available := errorSummaryMaxRunes - utf8.RuneCountInString(truncatedErrorSummarySuffix)
	cutoff := 0
	for range available {
		_, width := utf8.DecodeRuneInString(summary[cutoff:])
		cutoff += width
	}
	return summary[:cutoff] + truncatedErrorSummarySuffix
}
