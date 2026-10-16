package log

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/suite"
)

type summaryError struct {
	full    string
	summary string
}

func (e *summaryError) Error() string      { return e.full }
func (e *summaryError) LogSummary() string { return e.summary }

type ErrorForLogTestSuite struct {
	suite.Suite
}

func TestErrorForLogTestSuite(t *testing.T) {
	suite.Run(t, new(ErrorForLogTestSuite))
}

func (s *ErrorForLogTestSuite) TestNilAndEmpty() {
	s.Equal("<nil>", ErrorForLog(nil))
	s.Empty(ErrorForLog(errors.New("")))
}

func (s *ErrorForLogTestSuite) TestWrappedTypedFallbackAndMultiline() {
	typed := &summaryError{
		full:    "cause\n{\"level\":\"debug\",\"msg\":\"a\"}\n{\"level\":\"debug\",\"msg\":\"b\"}",
		summary: "cause",
	}
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", typed))
	s.Equal("cause", ErrorForLog(typed))
	s.Equal("cause", ErrorForLog(wrapped), "provider summary wins over wrapper text")

	multiline := errors.New("first\nsecond\r\nthird ")
	s.Equal("first\nsecond\r\nthird ", ErrorForLog(multiline), "no trimming or first-line cut")
}

func (s *ErrorForLogTestSuite) TestTypedSummaryIsBounded() {
	typed := &summaryError{full: "x", summary: strings.Repeat("a", 5000)}
	got := ErrorForLog(fmt.Errorf("wrap: %w", typed))
	s.Equal(errorSummaryMaxRunes, utf8.RuneCountInString(got))
	s.True(strings.HasSuffix(got, truncatedErrorSummarySuffix))
}

func (s *ErrorForLogTestSuite) TestTruncationBoundaries() {
	suffixRunes := utf8.RuneCountInString(truncatedErrorSummarySuffix)
	for _, tc := range []struct {
		name      string
		runes     int
		truncated bool
	}{
		{"2047", errorSummaryMaxRunes - 1, false},
		{"2048", errorSummaryMaxRunes, false},
		{"2049", errorSummaryMaxRunes + 1, true},
	} {
		s.Run(tc.name, func() {
			input := strings.Repeat("a", tc.runes)
			got := truncateErrorSummary(input)
			if !tc.truncated {
				s.Equal(input, got)
				return
			}
			s.Equal(errorSummaryMaxRunes, utf8.RuneCountInString(got))
			s.Equal(
				strings.Repeat("a", errorSummaryMaxRunes-suffixRunes)+truncatedErrorSummarySuffix,
				got,
			)
		})
	}
}

func (s *ErrorForLogTestSuite) TestTruncationIsRuneSafe() {
	input := strings.Repeat("é世🙂", 1000) // mixed 2/3/4-byte runes
	got := truncateErrorSummary(input)
	s.True(utf8.ValidString(got))
	s.Equal(errorSummaryMaxRunes, utf8.RuneCountInString(got))
	prefix := strings.TrimSuffix(got, truncatedErrorSummarySuffix)
	s.Equal(errorSummaryMaxRunes-utf8.RuneCountInString(truncatedErrorSummarySuffix),
		utf8.RuneCountInString(prefix))
	s.True(strings.HasPrefix(input, prefix))
}

func (s *ErrorForLogTestSuite) TestInvalidUTF8IsNormalizedOnlyInSummary() {
	raw := "bad\xff\xfebytes"
	err := errors.New(raw)
	got := ErrorForLog(err)
	s.Equal("bad\uFFFDbytes", got)
	s.Equal(raw, err.Error(), "original error text is not mutated")

	// Each invalid run becomes one replacement rune before counting.
	long := truncateErrorSummary(strings.Repeat("a\xff", 3000))
	s.True(utf8.ValidString(long))
	s.Equal(errorSummaryMaxRunes, utf8.RuneCountInString(long))
}
