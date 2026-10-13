package log

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	zapbuffer "go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

type TextRecordEncoderSuite struct{ suite.Suite }

func TestTextRecordEncoderSuite(t *testing.T) { suite.Run(t, new(TextRecordEncoderSuite)) }

var fixedEntryTime = time.Date(2026, 10, 13, 22, 41, 50, 98_000_000, time.UTC)

func testEntry(message string) zapcore.Entry {
	return zapcore.Entry{Level: zapcore.DebugLevel, Time: fixedEntryTime, Message: message}
}

func (s *TextRecordEncoderSuite) encode(
	enc zapcore.Encoder,
	entry zapcore.Entry,
	fields ...zapcore.Field,
) string {
	buf, err := enc.EncodeEntry(entry, fields)
	s.Require().NoError(err)
	out := buf.String()
	buf.Free()
	return out
}

func (s *TextRecordEncoderSuite) plainEncoder() zapcore.Encoder {
	return newOneLineTextEncoder(zapcore.NewConsoleEncoder(plainEncoderConfig()))
}

// requireOneRecord asserts out is one LF-terminated line and returns it without the LF.
func (s *TextRecordEncoderSuite) requireOneRecord(out string) string {
	s.Require().True(strings.HasSuffix(out, "\n"), "record must end in LF: %q", out)
	body := strings.TrimSuffix(out, "\n")
	s.Equal(1, strings.Count(out, "\n"), "exactly one LF: %q", out)
	for _, r := range body {
		s.False(r == '\r' || r == '\v' || r == '\f' || r == 0 || r == 0x7f ||
			r == 0x85 || r == 0x2028 || r == 0x2029, "forbidden rune %U in %q", r, out)
	}
	return body
}

func (s *TextRecordEncoderSuite) TestEscapesPhysicalLineBreaks() {
	out := s.encode(s.plainEncoder(), testEntry("first\nsecond\rthird\r\nfourth"))

	body := s.requireOneRecord(out)
	s.Equal(
		"2026-10-13T22:41:50.098Z\tDEBUG\tfirst\\nsecond\\rthird\\r\\nfourth",
		body,
	)
}

func (s *TextRecordEncoderSuite) TestEscapesControlsAndUnicodeSeparators() {
	out := s.encode(
		s.plainEncoder(),
		testEntry("a\x00b\vc\fd\x7fe\x01f\u0085g\u2028h\u2029i héllo \t|"),
	)

	body := s.requireOneRecord(out)
	s.Contains(
		body,
		`a\x00b\vc\fd\x7fe\x01f\u0085g\u2028h\u2029i héllo `+"\t|",
	)
}

func (s *TextRecordEncoderSuite) TestStackAndFields() {
	entry := testEntry("msg")
	entry.Stack = "frame1\nframe2"

	out := s.encode(s.plainEncoder(), entry, zap.String("detail", "x\ny"))

	body := s.requireOneRecord(out)
	s.Contains(body, `frame1\nframe2`)
	s.Contains(body, `"detail": "x\ny"`)
}

func (s *TextRecordEncoderSuite) TestCloneKeepsWithFieldsIsolated() {
	base := s.plainEncoder()
	derived := base.Clone()
	zap.String("child", "a\nb").AddTo(derived)

	derivedOut := s.requireOneRecord(s.encode(derived, testEntry("msg")))
	baseOut := s.requireOneRecord(s.encode(base, testEntry("msg")))

	s.Contains(derivedOut, `"child": "a\nb"`)
	s.NotContains(baseOut, "child")
}

func (s *TextRecordEncoderSuite) TestCleanRecordMatchesInnerEncoder() {
	inner := zapcore.NewConsoleEncoder(plainEncoderConfig())
	wrapped := newOneLineTextEncoder(zapcore.NewConsoleEncoder(plainEncoderConfig()))
	entry := testEntry("ordinary message with unicode héllo")

	s.Equal(s.encode(inner, entry), s.encode(wrapped, entry))
}

func (s *TextRecordEncoderSuite) TestColorEncoderPreservesANSIAndEscapesPayload() {
	enc := newOneLineTextEncoder(zapcore.NewConsoleEncoder(colorEncoderConfig()))

	out := s.encode(enc, testEntry("one\ntwo"))

	s.Equal(1, strings.Count(out, "\n"))
	s.True(strings.HasSuffix(out, `one\ntwo`+"\n"))
	s.Contains(out, "\x1b[")
	s.Contains(out, "\x1b[0m")
}

// fakeEncoder returns canned inner output.
type fakeEncoder struct {
	zapcore.Encoder
	pool zapbuffer.Pool
	out  *string
	err  error
}

func (f *fakeEncoder) EncodeEntry(zapcore.Entry, []zapcore.Field) (*zapbuffer.Buffer, error) {
	buf := f.pool.Get()
	if f.out != nil {
		_, _ = buf.WriteString(*f.out)
	}
	return buf, f.err
}

func (s *TextRecordEncoderSuite) TestTerminatorNormalization() {
	tests := []struct{ name, inner, want string }{
		{"surplus final LF", "hello\n\n", "hello\\n\n"},
		{"missing final LF", "hello", "hello\n"},
		{"empty", "", "\n"},
		{"only LF", "\n", "\n"},
		{"CRLF terminator", "hello\r\n", "hello\\r\n"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			inner := tt.inner
			enc := newOneLineTextEncoder(&fakeEncoder{pool: zapbuffer.NewPool(), out: &inner})
			s.Equal(tt.want, s.encode(enc, testEntry("ignored")))
		})
	}
}

func (s *TextRecordEncoderSuite) TestInnerErrorIsReturned() {
	boom := errors.New("boom")
	enc := newOneLineTextEncoder(&fakeEncoder{pool: zapbuffer.NewPool(), err: boom})
	buf, err := enc.EncodeEntry(testEntry("x"), nil)
	s.Nil(buf)
	s.ErrorIs(err, boom)
}

func (s *TextRecordEncoderSuite) TestInvalidAndTruncatedUTF8DoNotPanic() {
	out := s.encode(s.plainEncoder(), testEntry("bad\xff\xc2 tail\xe2\x80\n\xe2"))

	s.Equal(1, strings.Count(out, "\n"))
	s.Contains(out, "bad\xff\xc2 tail\xe2\x80\\n\xe2")
}
