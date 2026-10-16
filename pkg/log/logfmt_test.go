package log

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type LogfmtQuotingSuite struct{ suite.Suite }

func TestLogfmtQuotingSuite(t *testing.T) { suite.Run(t, new(LogfmtQuotingSuite)) }

func (s *LogfmtQuotingSuite) TestSafeTokenStaysBare() {
	s.Equal("plain-token_1.2/x:y", quoteLogfmt("plain-token_1.2/x:y"))
}

func (s *LogfmtQuotingSuite) TestControlQuoting() {
	for name, value := range map[string]string{
		"empty":          "",
		"CR only":        "a\rb",
		"LF":             "a\nb",
		"space":          "a b",
		"tab":            "a\tb",
		"vertical tab":   "a\vb",
		"form feed":      "a\fb",
		"NUL":            "a\x00b",
		"DEL":            "a\x7fb",
		"C1 NEL":         "a\u0085b",
		"line separator": "a\u2028b",
		"para separator": "a\u2029b",
		"no-break space": "a\u00a0b",
		"backslash":      `a\b`,
		"quote":          `a"b`,
		"equals":         "a=b",
		"ANSI escape":    "a\x1b[31mb",
	} {
		s.Run(name, func() {
			quoted := quoteLogfmt(value)
			s.True(strings.HasPrefix(quoted, `"`) && strings.HasSuffix(quoted, `"`), quoted)
			s.NotContains(quoted, "\n")
			s.NotContains(quoted, "\r")
			unquoted, err := strconv.Unquote(quoted)
			s.Require().NoError(err)
			s.Equal(value, unquoted)
		})
	}
}

func (s *LogfmtQuotingSuite) TestEncodedRecordKeepsLayoutAndOneLine() {
	enc := newLogfmtEncoder()
	buf, err := enc.EncodeEntry(testEntry("cr\ronly"), []zap.Field{zap.String("b", "x\ry"), zap.String("a", "ok")})
	s.Require().NoError(err)
	out := buf.String()
	buf.Free()

	s.Equal(1, strings.Count(out, "\n"))
	s.NotContains(out, "\r")
	s.Equal(`level=debug ts=2026-10-13T22:41:50Z msg="cr\ronly" a=ok b="x\ry"`+"\n", out)
}
