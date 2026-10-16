package log

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	errorSummaryMaxRunes        = 2048
	truncatedErrorSummarySuffix = "...[truncated]"
)

// LogSummaryProvider is implemented by errors whose Error text carries data
// that must not be replayed into a log record, such as a diagnostic tail that
// was already emitted separately.
type LogSummaryProvider interface {
	LogSummary() string
}

// ErrorForLog returns a log-safe description of err, capped at 2048 runes
// including the truncation suffix. The first LogSummaryProvider found while
// unwrapping err supplies the summary; otherwise err.Error() is used.
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
	keep := errorSummaryMaxRunes - utf8.RuneCountInString(truncatedErrorSummarySuffix)
	cutoff := 0
	for range keep {
		_, width := utf8.DecodeRuneInString(summary[cutoff:])
		cutoff += width
	}
	return summary[:cutoff] + truncatedErrorSummarySuffix
}
