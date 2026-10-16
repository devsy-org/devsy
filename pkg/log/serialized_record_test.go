package log

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

// The logger is package-global, so these tests must not run in parallel.
type SerializedRecordSuite struct{ suite.Suite }

func TestSerializedRecordSuite(t *testing.T) { suite.Run(t, new(SerializedRecordSuite)) }

func (s *SerializedRecordSuite) SetupTest() {
	s.T().Setenv("NO_COLOR", "1")
	s.T().Cleanup(func() { sugar.Store(zap.NewNop().Sugar()) })
}

// capture returns what emit wrote to the AddSink sink and to stderr.
func (s *SerializedRecordSuite) capture(cfg Config, emit func()) (sink, stderr string) {
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous }()

	cfg.Verbosity = 2
	Init(cfg)
	var sinkBuf syncBuffer
	remove := AddSink(&sinkBuf)
	defer remove()

	emit()
	_ = Sync() // fsync on a pipe fails with EBADF; the write itself is synchronous
	os.Stderr = previous
	s.Require().NoError(writer.Close())
	data, err := io.ReadAll(reader)
	s.Require().NoError(err)
	s.Require().NoError(reader.Close())
	return sinkBuf.String(), string(data)
}

func (s *SerializedRecordSuite) TestTextMultilineErrorIsOnePhysicalRecord() {
	tunnelErr := errors.New(
		"run agent command failed: exit status 1\n" +
			`{"level":"debug","ts":"2026-10-13T22:41:48.610Z","msg":"incoming client connection: path=/"}` + "\n" +
			`{"level":"debug","ts":"2026-10-13T22:41:48.610Z","msg":"credentials server started"}` + "\n" +
			`{"kind":"error","outcome":"error","code":"UNKNOWN","message":"command failed with exit code 1"}`,
	)

	sink, stderr := s.capture(Config{Format: "text"}, func() {
		Debugf(
			"pipe bridge completed: tunnel_err=%v handler_err=%v parent_err=%v pair_err=%v",
			tunnelErr, nil, nil, errors.New("context canceled"),
		)
	})

	for name, out := range map[string]string{"sink": sink, "stderr": stderr} {
		s.Equal(1, strings.Count(out, "\n"), "%s must be one physical record: %q", name, out)
		s.True(strings.HasSuffix(out, "\n"), name)
		s.Contains(out, "\tDEBUG\tpipe bridge completed: tunnel_err=run agent command failed: exit status 1\\n{")
		s.Contains(out, "pair_err=context canceled")
	}
	s.Equal(stderr, sink)
}

func (s *SerializedRecordSuite) TestEveryFormatEmitsOneRecordPerEvent() {
	const message = "alpha\nbeta\r\ngamma\rdelta\u2028epsilon\u0085zeta\v\f\x00\x7f\x01 end"
	for _, format := range []string{"text", "json", "logfmt"} {
		s.Run(format, func() {
			sink, stderr := s.capture(Config{Format: format}, func() {
				Infof("%s", message)
				Infow("fields", "detail", message)
			})
			s.Equal(sink, stderr)
			lines := strings.Split(strings.TrimSuffix(sink, "\n"), "\n")
			s.Require().Len(lines, 2, "two events, two physical lines: %q", sink)
			s.NotContains(sink, "\r")
			s.assertRecordContent(format, lines, message)
		})
	}
}

func (s *SerializedRecordSuite) assertRecordContent(format string, lines []string, message string) {
	switch format {
	case "json":
		for i, line := range lines {
			var decoded map[string]any
			s.Require().NoError(json.Unmarshal([]byte(line), &decoded))
			if i == 0 {
				s.Equal(message, decoded["msg"], "JSON must round-trip the logical message")
			} else {
				s.Equal(message, decoded["detail"])
			}
		}
	case "logfmt":
		s.Contains(lines[0], " msg=")
		quoted := lines[0][strings.Index(lines[0], " msg=")+len(" msg="):]
		unquoted, err := strconv.Unquote(quoted)
		s.Require().NoError(err)
		s.Equal(message, unquoted)
	default:
		s.Contains(lines[0], `alpha\nbeta\r\ngamma\rdelta\u2028epsilon\u0085zeta\v\f\x00\x7f\x01 end`)
		s.Contains(lines[1], `alpha\nbeta`)
	}
}

func (s *SerializedRecordSuite) TestConcurrentEventsStayOnePhysicalRecordEach() {
	sink, _ := s.capture(Config{Format: "text"}, func() {
		done := make(chan struct{})
		for i := range 8 {
			go func() {
				defer func() { done <- struct{}{} }()
				for j := range 25 {
					Infof("worker %d event %d\nsecond line", i, j)
				}
			}()
		}
		for range 8 {
			<-done
		}
	})

	lines := strings.Split(strings.TrimSuffix(sink, "\n"), "\n")
	s.Len(lines, 200)
	for _, line := range lines {
		s.Contains(line, `\nsecond line`)
	}
}

const redactionSecret = "line1\nli\"ne2\r\\end\x00\v\f\x01\u2028"

// Encoders rewrite control characters before the writers redact, so escaped
// forms of the secret must be masked too.
func (s *SerializedRecordSuite) TestRedactionAcrossFormats() {
	for _, format := range []string{"text", "json", "logfmt"} {
		s.Run(format, func() {
			sink, stderr := s.capture(Config{
				Format:   format,
				Redactor: secrets.NewRedactor([]string{"TOKEN=" + redactionSecret}),
			}, func() {
				Infof("token=%s", redactionSecret)
				Infow("with field", "credential", redactionSecret)
				Errorf("%v", errors.New(redactionSecret))
			})

			s.Equal(sink, stderr, "stderr and AddSink must agree")
			s.Contains(sink, "***")
			s.assertNoSecretFragments(sink)
			s.Equal(3, strings.Count(sink, "\n"))
		})
	}
}

func (s *SerializedRecordSuite) assertNoSecretFragments(out string) {
	for _, fragment := range []string{
		"line1", "li\"ne2", `li\"ne2`, "ne2", "end", "\x00", "\\x00", "\\u0000",
		"\u2028", `\u2028`, "\\x01", "\\u0001",
	} {
		s.NotContains(out, fragment, "secret fragment %q leaked", fragment)
	}
}

func (s *SerializedRecordSuite) TestRedactionCoversEveryControlByte() {
	chars := make([]string, 0, 0x80+3)
	for b := range 0x80 {
		chars = append(chars, string(rune(b)))
	}
	chars = append(chars, "\u0085", "\u2028", "\u2029", "\xff")

	for _, format := range []string{"text", "json", "logfmt"} {
		for _, char := range chars {
			secret := "SECRETHEAD" + char + "SECRETTAIL"
			sink, _ := s.capture(Config{
				Format:   format,
				Redactor: secrets.NewRedactor([]string{"TOKEN=" + secret}),
			}, func() {
				Infof("m=%s", secret)
				Infow("f", "k", secret)
			})
			s.NotContains(sink, "SECRETHEAD", "format %s char %q leaked head: %q", format, char, sink)
			s.NotContains(sink, "SECRETTAIL", "format %s char %q leaked tail: %q", format, char, sink)
			s.Contains(sink, "***", "format %s char %q", format, char)
		}
	}
}

func (s *SerializedRecordSuite) TestEnvironmentSecretWithLineBreakIsMasked() {
	s.T().Setenv("DEVSY_LOG_TEST_SECRET", "env\nsecret-846297")

	sink, stderr := s.capture(Config{Format: "text"}, func() {
		Infof("value=%s", "env\nsecret-846297")
	})

	s.Equal(sink, stderr)
	s.NotContains(sink, "secret-846297")
	s.Contains(sink, "value=***")
}
