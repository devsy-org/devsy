package inject

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// ---------------------------------------------------------------------------
// PipeTestSuite
// ---------------------------------------------------------------------------

type PipeTestSuite struct {
	suite.Suite
}

func TestPipeSuite(t *testing.T) {
	suite.Run(t, new(PipeTestSuite))
}

func (s *PipeTestSuite) TestPipe_NormalBidirectionalCopy() {
	toStdinReader, toStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	// Use bytes.Buffer for toStdout — pipe() doesn't close it (just io.Writer),
	// and the io.Pipe feeder below guarantees the stdout copy goroutine writes
	// to the buffer before sending on errChan, giving a happens-before with
	// pipe()'s return.
	var toStdoutBuf bytes.Buffer

	// Use io.Pipe for feeders — synchronous Write blocks until the copy
	// goroutine Reads, ensuring both goroutines have consumed and written
	// their data before either signals completion. The sequential close
	// order (stdout first, then stdin) makes the stdout direction always
	// win the race to errChan.
	fromStdinReader, fromStdinWriter := io.Pipe()
	fromStdoutReader, fromStdoutWriter := io.Pipe()
	defer func() { _ = fromStdinWriter.Close() }()
	defer func() { _ = fromStdoutWriter.Close() }()

	stdinPayload := "hello from stdin"
	stdoutPayload := "hello from stdout"

	go func() {
		_, _ = fromStdoutWriter.Write([]byte(stdoutPayload))
		_, _ = fromStdinWriter.Write([]byte(stdinPayload))
		_ = fromStdoutWriter.Close()
		_ = fromStdinWriter.Close()
	}()

	// Read from toStdin consumer in the background — unblocks when pipe()
	// closes toStdinWriter.
	var gotStdin []byte
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		gotStdin, _ = io.ReadAll(toStdinReader)
	}()

	pipeErr := pipe(toStdinWriter, fromStdinReader, &toStdoutBuf, fromStdoutReader)

	<-stdinDone
	_ = toStdinReader.Close()

	s.NoError(pipeErr)
	s.Equal(stdinPayload, string(gotStdin))
	s.Equal(stdoutPayload, toStdoutBuf.String())
}

func (s *PipeTestSuite) TestPipe_WriterSideClosesFirst() {
	// fromStdout reaches EOF first — pipe should return nil and toStdin should
	// end up closed.
	toStdinReader, toStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	// fromStdin: keep writer open so this direction does NOT finish first.
	fromStdinReader, fromStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	// toStdout: use a buffer (pipe won't close it).
	var toStdoutBuf bytes.Buffer

	fromStdoutReader, fromStdoutWriter, err := os.Pipe()
	s.Require().NoError(err)

	// Close fromStdout feeder immediately so that direction finishes first.
	_ = fromStdoutWriter.Close()

	pipeErr := pipe(toStdinWriter, fromStdinReader, &toStdoutBuf, fromStdoutReader)
	s.NoError(pipeErr)

	// toStdin should now be closed by pipe(); writing should fail.
	_, err = toStdinWriter.Write([]byte("should fail"))
	s.Error(err)

	// Clean up.
	_ = fromStdinWriter.Close()
	_ = toStdinReader.Close()
}

func (s *PipeTestSuite) TestPipe_ReaderSideClosesFirst() {
	// fromStdin reaches EOF first — pipe should return nil and fromStdout
	// should end up closed.
	toStdinReader, toStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	fromStdinReader, fromStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	// toStdout: use a buffer.
	var toStdoutBuf bytes.Buffer

	// fromStdout: keep writer open so this direction does NOT finish first.
	fromStdoutReader, fromStdoutWriter, err := os.Pipe()
	s.Require().NoError(err)

	// Close fromStdin feeder so that direction finishes first.
	_ = fromStdinWriter.Close()

	pipeErr := pipe(toStdinWriter, fromStdinReader, &toStdoutBuf, fromStdoutReader)
	s.NoError(pipeErr)

	// fromStdout should now be closed by pipe(); reading should fail.
	buf := make([]byte, 1)
	_, err = fromStdoutReader.Read(buf)
	s.Error(err)

	// Clean up.
	_ = fromStdoutWriter.Close()
	_ = toStdinReader.Close()
}

func (s *PipeTestSuite) TestPipe_ErrorPropagation() {
	writeErr := errors.New("write exploded")
	badWriter := &errWriter{err: writeErr}

	fromStdoutReader, fromStdoutWriter, err := os.Pipe()
	s.Require().NoError(err)

	// Feed data into fromStdout so io.Copy(badWriter, fromStdout) triggers the
	// write error.
	_, err = fromStdoutWriter.Write([]byte("data"))
	s.Require().NoError(err)
	_ = fromStdoutWriter.Close()

	// fromStdin: use an os.Pipe with the writer kept open so this direction
	// blocks on Read and does NOT finish before the error direction.
	fromStdinReader, fromStdinWriter, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = fromStdinWriter.Close() }()

	toStdinWriter := nopWriteCloser{}

	pipeErr := pipe(toStdinWriter, fromStdinReader, badWriter, fromStdoutReader)
	s.Error(pipeErr)
	s.Equal(writeErr, pipeErr)
}

func (s *PipeTestSuite) TestPipe_BothEndpointsClosedAfterReturn() {
	toStdinReader, toStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	fromStdinReader, fromStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	var toStdoutBuf bytes.Buffer

	fromStdoutReader, fromStdoutWriter, err := os.Pipe()
	s.Require().NoError(err)

	// Close both feeder ends so pipe completes quickly.
	_ = fromStdinWriter.Close()
	_ = fromStdoutWriter.Close()

	pipeErr := pipe(toStdinWriter, fromStdinReader, &toStdoutBuf, fromStdoutReader)
	s.NoError(pipeErr)

	// toStdin (WriteCloser) should be closed — write must fail.
	_, err = toStdinWriter.Write([]byte("x"))
	s.Error(err)

	// fromStdout (ReadCloser) should be closed — read must fail.
	buf := make([]byte, 1)
	_, err = fromStdoutReader.Read(buf)
	s.Error(err)

	// Clean up consumer end.
	_ = toStdinReader.Close()
}

func (s *PipeTestSuite) TestPipe_NoGoroutineLeak() {
	// Let goroutine counts settle.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	toStdinReader, toStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	fromStdinReader, fromStdinWriter, err := os.Pipe()
	s.Require().NoError(err)

	var toStdoutBuf bytes.Buffer

	fromStdoutReader, fromStdoutWriter, err := os.Pipe()
	s.Require().NoError(err)

	// Close feeders so pipe completes quickly.
	_ = fromStdinWriter.Close()
	_ = fromStdoutWriter.Close()

	pipeErr := pipe(toStdinWriter, fromStdinReader, &toStdoutBuf, fromStdoutReader)
	s.NoError(pipeErr)

	// Clean up consumer end.
	_ = toStdinReader.Close()

	// Give goroutines time to exit.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	after := runtime.NumGoroutine()
	s.LessOrEqual(
		after, baseline+2,
		"goroutine count grew by more than 2: before=%d after=%d", baseline, after,
	)
}

// ---------------------------------------------------------------------------
// ReadLineTestSuite
// ---------------------------------------------------------------------------

type ReadLineTestSuite struct {
	suite.Suite
}

func TestReadLineSuite(t *testing.T) {
	suite.Run(t, new(ReadLineTestSuite))
}

func (s *ReadLineTestSuite) TestReadLine_NormalLine() {
	reader := strings.NewReader("hello\n")
	line, err := readLine(reader)
	s.NoError(err)
	s.Equal("hello", line)
}

func (s *ReadLineTestSuite) TestReadLine_MultipleLines() {
	reader := strings.NewReader("first\nsecond\n")
	line, err := readLine(reader)
	s.NoError(err)
	s.Equal("first", line)
}

func (s *ReadLineTestSuite) TestReadLine_EOFBeforeNewline() {
	reader := strings.NewReader("partial")
	line, err := readLine(reader)
	s.Error(err)
	s.Equal(io.EOF, err)
	s.Equal("", line)
}

func (s *ReadLineTestSuite) TestReadLine_EmptyReader() {
	reader := strings.NewReader("")
	line, err := readLine(reader)
	s.Error(err)
	s.Equal(io.EOF, err)
	s.Equal("", line)
}

// ---------------------------------------------------------------------------
// WaitForMessageTestSuite
// ---------------------------------------------------------------------------

type WaitForMessageTestSuite struct {
	suite.Suite
}

func TestWaitForMessageSuite(t *testing.T) {
	suite.Run(t, new(WaitForMessageTestSuite))
}

func (s *WaitForMessageTestSuite) TestWaitForMessage_Success() {
	ch := make(chan error, 1)
	ch <- nil
	err := waitForMessage(ch, time.Second)
	s.NoError(err)
}

func (s *WaitForMessageTestSuite) TestWaitForMessage_SuccessWithError() {
	expected := errors.New("something went wrong")
	ch := make(chan error, 1)
	ch <- expected
	err := waitForMessage(ch, time.Second)
	s.Error(err)
	s.Equal(expected, err)
}

func (s *WaitForMessageTestSuite) TestWaitForMessage_Timeout() {
	ch := make(chan error) // unbuffered, never sent to
	err := waitForMessage(ch, 10*time.Millisecond)
	s.Error(err)
	s.Equal(context.DeadlineExceeded, err)
}

// ---------------------------------------------------------------------------
// PerformMutualHandshakeTestSuite
// ---------------------------------------------------------------------------

type PerformMutualHandshakeTestSuite struct {
	suite.Suite
}

func TestPerformMutualHandshakeSuite(t *testing.T) {
	suite.Run(t, new(PerformMutualHandshakeTestSuite))
}

func (s *PerformMutualHandshakeTestSuite) TestPerformMutualHandshake_ValidPing() {
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = reader.Close() }()

	err = performMutualHandshake("ping", writer)
	s.NoError(err)
	_ = writer.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	s.NoError(err)
	s.Equal("pong\n", buf.String())
}

func (s *PerformMutualHandshakeTestSuite) TestPerformMutualHandshake_InvalidInput() {
	_, writer, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = writer.Close() }()

	err = performMutualHandshake("hello", writer)
	s.Error(err)
	s.Contains(err.Error(), "unexpected start line")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// errWriter is an io.Writer that always returns the configured error.
type errWriter struct {
	err error
}

func (w *errWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

// nopWriteCloser is an io.WriteCloser that discards writes.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
