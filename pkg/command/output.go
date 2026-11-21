package command

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/devsy-org/devsy/logging/redact"
)

const stderrCaptureSize = 32 << 10

const (
	IncludeFailureStdout = true
	ExcludeFailureStdout = false
)

func Output(cmd *exec.Cmd, includeStdout bool) ([]byte, error) {
	redactor := redact.New(redact.Options{Environment: cmd.Environ()})
	var capture *stderrCapture
	if cmd.Stderr == nil {
		capture = &stderrCapture{stream: redact.NewDiagnosticStream(redactor)}
		cmd.Stderr = capture
	}
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil
	}
	wrapped := &Error{err: err}
	if includeStdout {
		wrapped.stdout = stdout
	}
	stderr := maskJSONRecords(redactor, wrapped.stderr())
	if capture != nil {
		stderr = capture.finish(err, redactor)
	}
	wrapped.setDisplay(redactor, stderr)
	return stdout, wrapped
}

type stderrCapture struct {
	stream     *redact.Stream
	raw        boundedCapture
	masked     boundedCapture
	structured structuredLineStart
}

func (c *stderrCapture) Write(p []byte) (int, error) {
	text := string(p)
	c.raw.append(text)
	c.masked.append(c.stream.RedactChunk(text))
	c.structured.scan(text)
	return len(p), nil
}

func (c *stderrCapture) finish(cause error, redactor *redact.Redactor) string {
	raw := c.raw.text()
	if exit, ok := errors.AsType[*exec.ExitError](cause); ok {
		exit.Stderr = []byte(raw)
	}
	if c.raw.omitted == 0 {
		return maskJSONRecords(redactor, raw)
	}
	if c.structured.found || c.structured.isCandidate() {
		return redactor.Mask()
	}
	c.masked.append(c.stream.Flush())
	return c.masked.text()
}

type structuredLineStart struct {
	prefix [128]byte
	length int
	found  bool
	skip   bool
}

func (s *structuredLineStart) scan(text string) {
	for index := range len(text) {
		if s.found {
			return
		}
		value := text[index]
		if value == '\r' || value == '\n' {
			s.found = s.isCandidate()
			s.length = 0
			s.skip = false
			continue
		}
		if !s.skip {
			s.take(value)
		}
	}
}

func (s *structuredLineStart) take(value byte) {
	if s.length == 0 {
		switch value {
		case ' ', '\t':
			return
		case '[', '{':
		default:
			s.skip = true
			return
		}
	}
	s.prefix[s.length] = value
	s.length++
	if s.length == len(s.prefix) {
		s.found = s.isCandidate()
		s.skip = true
	}
}

func (s *structuredLineStart) isCandidate() bool {
	return s.length > 0 && !s.skip && looksLikeStructuredJSON(string(s.prefix[:s.length]))
}

type boundedCapture struct {
	prefix  []byte
	tail    []byte
	omitted int64
}

func (c *boundedCapture) append(text string) {
	first := min(stderrCaptureSize-len(c.prefix), len(text))
	c.prefix = append(c.prefix, text[:first]...)
	text = text[first:]
	if len(text) >= stderrCaptureSize {
		c.omitted += int64(len(c.tail) + len(text) - stderrCaptureSize)
		c.tail = append(c.tail[:0], text[len(text)-stderrCaptureSize:]...)
		return
	}
	if excess := len(c.tail) + len(text) - stderrCaptureSize; excess > 0 {
		c.omitted += int64(excess)
		c.tail = c.tail[:copy(c.tail, c.tail[excess:])]
	}
	c.tail = append(c.tail, text...)
}

func (c *boundedCapture) text() string {
	if c.omitted == 0 {
		return string(c.prefix) + string(c.tail)
	}
	return fmt.Sprintf("%s\n... omitting %d bytes ...\n%s", c.prefix, c.omitted, c.tail)
}
