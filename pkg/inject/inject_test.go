package inject

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
)

// errWriter is an io.Writer that always returns a configured error.
type errWriter struct {
	err error
}

func (w *errWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}

// nopWriteCloser wraps an io.Writer with a no-op Close.
type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

// --- PipeTestSuite ---

type PipeTestSuite struct {
	suite.Suite
}

func (s *PipeTestSuite) TestPipe_NormalBidirectionalCopy() {
	fromStdinReader, fromStdinWriter := io.Pipe()
	toStdoutBuf := &bytes.Buffer{}
	toStdinBuf := &bytes.Buffer{}

	fromStdoutReader, fromStdoutWriter := io.Pipe()
	toStdinPipeReader, toStdinPipeWriter := io.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- pipe(toStdinPipeWriter, fromStdinReader, toStdoutBuf, fromStdoutReader)
	}()

	_, err := fromStdinWriter.Write([]byte("hello from stdin"))
	s.Require().NoError(err)
	_ = fromStdinWriter.Close()

	_, err = fromStdoutWriter.Write([]byte("hello from stdout"))
	s.Require().NoError(err)
	_ = fromStdoutWriter.Close()

	received, err := io.ReadAll(toStdinPipeReader)
	s.Require().NoError(err)
	toStdinBuf.Write(received)

	s.NoError(<-errCh)
	s.Equal("hello from stdin", toStdinBuf.String())
	s.Equal("hello from stdout", toStdoutBuf.String())
}

func (s *PipeTestSuite) TestPipe_WriterSideClosesFirst() {
	stdinReader, stdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	stdoutReader, stdoutWriter, err := os.Pipe()
	s.Require().NoError(err)
	_ = stdoutWriter.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- pipe(stdinWriter, strings.NewReader("data"), io.Discard, stdoutReader)
	}()

	s.NoError(<-errCh)
	_ = stdinReader.Close()
}

func (s *PipeTestSuite) TestPipe_ReaderSideClosesFirst() {
	_, stdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	pr, pw := io.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- pipe(stdinWriter, &bytes.Buffer{}, io.Discard, pr)
	}()

	_ = pw.Close()
	s.NoError(<-errCh)
}

func (s *PipeTestSuite) TestPipe_ErrorPropagation() {
	expectedErr := errors.New("write boom")
	toStdin := nopWriteCloser{&errWriter{err: expectedErr}}

	fromStdoutReader, fromStdoutWriter := io.Pipe()
	go func() {
		_, _ = fromStdoutWriter.Write([]byte("data"))
		_ = fromStdoutWriter.Close()
	}()

	err := pipe(toStdin, strings.NewReader("trigger write"), io.Discard, fromStdoutReader)
	s.ErrorIs(err, expectedErr)
}

func (s *PipeTestSuite) TestPipe_BothEndpointsClosedAfterReturn() {
	stdinReader, stdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	stdoutReader, stdoutWriter, err := os.Pipe()
	s.Require().NoError(err)
	_ = stdoutWriter.Close()

	err = pipe(stdinWriter, &bytes.Buffer{}, io.Discard, stdoutReader)
	s.NoError(err)

	_, writeErr := stdinWriter.Write([]byte("test"))
	s.Error(writeErr, "stdinWriter should be closed after pipe returns")

	buf := make([]byte, 1)
	_, readErr := stdoutReader.Read(buf)
	s.Error(readErr, "stdoutReader should be closed after pipe returns")

	_ = stdinReader.Close()
}

func (s *PipeTestSuite) TestPipe_NoGoroutineLeak() {
	before := runtime.NumGoroutine()

	for range 10 {
		stdinReader, stdinWriter, err := os.Pipe()
		s.Require().NoError(err)

		stdoutReader, stdoutWriter, err := os.Pipe()
		s.Require().NoError(err)
		_ = stdoutWriter.Close()

		_ = pipe(stdinWriter, &bytes.Buffer{}, io.Discard, stdoutReader)
		_ = stdinReader.Close()
	}

	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	s.LessOrEqual(after, before+5, "goroutine leak detected: before=%d after=%d", before, after)
}

func (s *PipeTestSuite) TestPipe_ConcurrentCopyRaceRegression() {
	for i := range 100 {
		func() {
			fromStdinReader, fromStdinWriter := io.Pipe()
			toStdoutBuf := &bytes.Buffer{}

			fromStdoutReader, fromStdoutWriter := io.Pipe()
			toStdinPipeReader, toStdinPipeWriter := io.Pipe()

			errCh := make(chan error, 1)
			go func() {
				errCh <- pipe(toStdinPipeWriter, fromStdinReader, toStdoutBuf, fromStdoutReader)
			}()

			msg := fmt.Sprintf("iteration-%d", i)
			_, err := fromStdinWriter.Write([]byte(msg))
			s.Require().NoError(err)
			_ = fromStdinWriter.Close()

			_, err = fromStdoutWriter.Write([]byte(msg))
			s.Require().NoError(err)
			_ = fromStdoutWriter.Close()

			received, err := io.ReadAll(toStdinPipeReader)
			s.Require().NoError(err)

			s.NoError(<-errCh)
			s.Equal(msg, string(received), "stdin data lost at iteration %d", i)
			s.Equal(msg, toStdoutBuf.String(), "stdout data lost at iteration %d", i)
		}()
	}
}

func TestPipeSuite(t *testing.T) {
	suite.Run(t, new(PipeTestSuite))
}

// --- ReadLineTestSuite ---

type ReadLineTestSuite struct {
	suite.Suite
}

func (s *ReadLineTestSuite) TestReadLine_NormalLine() {
	r := strings.NewReader("hello\n")
	line, err := readLine(r)
	s.NoError(err)
	s.Equal("hello", line)
}

func (s *ReadLineTestSuite) TestReadLine_MultipleLines() {
	r := strings.NewReader("first\nsecond\n")
	line, err := readLine(r)
	s.NoError(err)
	s.Equal("first", line)
}

func (s *ReadLineTestSuite) TestReadLine_EOFBeforeNewline() {
	r := strings.NewReader("no newline")
	_, err := readLine(r)
	s.ErrorIs(err, io.EOF)
}

func (s *ReadLineTestSuite) TestReadLine_EmptyReader() {
	r := strings.NewReader("")
	_, err := readLine(r)
	s.ErrorIs(err, io.EOF)
}

func TestReadLineSuite(t *testing.T) {
	suite.Run(t, new(ReadLineTestSuite))
}

// --- WaitForMessageTestSuite ---

type WaitForMessageTestSuite struct {
	suite.Suite
}

func (s *WaitForMessageTestSuite) TestWaitForMessage_Success() {
	ch := make(chan error, 1)
	ch <- nil
	err := waitForMessage(ch, time.Second)
	s.NoError(err)
}

func (s *WaitForMessageTestSuite) TestWaitForMessage_SuccessWithError() {
	ch := make(chan error, 1)
	expected := errors.New("something failed")
	ch <- expected
	err := waitForMessage(ch, time.Second)
	s.ErrorIs(err, expected)
}

func (s *WaitForMessageTestSuite) TestWaitForMessage_Timeout() {
	ch := make(chan error)
	err := waitForMessage(ch, 10*time.Millisecond)
	s.ErrorIs(err, context.DeadlineExceeded)
}

func TestWaitForMessageSuite(t *testing.T) {
	suite.Run(t, new(WaitForMessageTestSuite))
}

// --- PerformMutualHandshakeTestSuite ---

type PerformMutualHandshakeTestSuite struct {
	suite.Suite
}

func (s *PerformMutualHandshakeTestSuite) TestPerformMutualHandshake_ValidPing() {
	buf := &bytes.Buffer{}
	wc := nopWriteCloser{buf}
	err := performMutualHandshake("ping\n", wc)
	s.NoError(err)
	s.Equal("pong\n", buf.String())
}

func (s *PerformMutualHandshakeTestSuite) TestPerformMutualHandshake_InvalidInput() {
	buf := &bytes.Buffer{}
	wc := nopWriteCloser{buf}
	err := performMutualHandshake("hello\n", wc)
	s.Error(err)
	s.Contains(err.Error(), "unexpected start line")
}

func TestPerformMutualHandshakeSuite(t *testing.T) {
	suite.Run(t, new(PerformMutualHandshakeTestSuite))
}

// --- InjectTimingLogTestSuite ---

type InjectTimingLogTestSuite struct {
	suite.Suite
}

func (s *InjectTimingLogTestSuite) TestInject_TimingLogs() {
	logs := log.InitTestObserved(s.T(), zapcore.DebugLevel)

	ctx := context.Background()

	execFunc := func(_ context.Context, _ string, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
		// Simulate the inject.sh protocol: send ping, read pong, send done.
		if _, err := stdout.Write([]byte("ping\n")); err != nil {
			return err
		}

		buf := make([]byte, 64)
		n, err := stdin.Read(buf)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(buf[:n])) != "pong" {
			return fmt.Errorf("expected pong, got %q", string(buf[:n]))
		}

		if _, err := stdout.Write([]byte("done\n")); err != nil {
			return err
		}

		// Return immediately so stdout pipe closes and pipe() completes quickly.
		return nil
	}

	wasExecuted, err := Inject(InjectOptions{
		Ctx:  ctx,
		Exec: execFunc,
		ScriptParams: &Params{
			Command:         "test-cmd",
			AgentRemotePath: "/tmp/agent",
			DownloadURLs:    &DownloadURLs{},
		},
		Stdin:   strings.NewReader(""),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Timeout: 5 * time.Second,
	})

	s.NoError(err)
	s.True(wasExecuted)

	messages := make([]string, 0, len(logs.All()))
	for _, entry := range logs.All() {
		messages = append(messages, entry.Message)
	}

	s.Contains(messages, "injection: start")
	s.True(
		containsPrefix(messages, "injection: payload delivered elapsed="),
		"missing payload log: %v", messages,
	)
	s.True(
		containsPrefix(messages, "injection: complete elapsed="),
		"missing complete log: %v", messages,
	)
	s.True(
		containsPrefix(messages, "injection: handshake complete elapsed="),
		"missing handshake log: %v", messages,
	)
}

func TestInjectTimingLogSuite(t *testing.T) {
	suite.Run(t, new(InjectTimingLogTestSuite))
}

func containsPrefix(messages []string, prefix string) bool {
	for _, msg := range messages {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}
