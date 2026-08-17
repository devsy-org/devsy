package inject

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/util/goleaktest"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
)

// TestMain sets up the test environment and checks for goroutine leaks after tests.
func TestMain(m *testing.M) {
	goleaktest.TestMain(m)
}

const (
	testCommand         = "test-command"
	testExistsCheck     = "true"
	testAgentRemotePath = "/tmp/agent"
	testPong            = "pong\n"
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

type ReadLineTestSuite struct {
	suite.Suite
}

// readStringClose wraps a strings.Reader as an io.ReadCloser (no-op Close).
type readStringClose struct {
	*strings.Reader
}

func (readStringClose) Close() error { return nil }

func (s *ReadLineTestSuite) TestReadLine_NormalLine() {
	line, err := readLine(context.Background(), readStringClose{strings.NewReader("hello\n")})
	s.NoError(err)
	s.Equal("hello", line)
}

func (s *ReadLineTestSuite) TestReadLine_MultipleLinesReadsFirstOnly() {
	line, err := readLine(
		context.Background(),
		readStringClose{strings.NewReader("first\nsecond\n")},
	)
	s.NoError(err)
	s.Equal("first", line)
}

func (s *ReadLineTestSuite) TestReadLine_EOFBeforeNewlineReturnsPartialAndErr() {
	line, err := readLine(context.Background(), readStringClose{strings.NewReader("no newline")})
	s.ErrorIs(err, io.EOF)
	s.Equal("no newline", line)
}

func (s *ReadLineTestSuite) TestReadLine_EmptyReader() {
	_, err := readLine(context.Background(), readStringClose{strings.NewReader("")})
	s.ErrorIs(err, io.EOF)
}

func (s *ReadLineTestSuite) TestReadLine_ContextCancelAbortsAndClosesReader() {
	r, w, err := os.Pipe()
	s.Require().NoError(err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = readLine(ctx, r)
	s.ErrorIs(err, context.DeadlineExceeded)

	_, writeErr := w.Write([]byte("x"))
	s.Error(writeErr, "reader should be closed after cancel")
}

type BinarySizeTestSuite struct {
	suite.Suite
}

func (s *BinarySizeTestSuite) TestBinarySize_OsFileReportsStatSize() {
	f, err := os.CreateTemp("", "inject-binary-*")
	s.Require().NoError(err)
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()

	s.Require().NoError(f.Sync())
	size, ok := binarySize(f)
	s.True(ok)
	s.Equal(int64(0), size)
}

func (s *BinarySizeTestSuite) TestBinarySize_InMemoryReaderIsNotStatable() {
	_, ok := binarySize(strings.NewReader("not a file"))
	s.False(ok)
}

func TestReadLineSuite(t *testing.T) {
	suite.Run(t, new(ReadLineTestSuite))
}

func TestBinarySizeSuite(t *testing.T) {
	suite.Run(t, new(BinarySizeTestSuite))
}

type InjectTimingLogTestSuite struct {
	suite.Suite
}

func (s *InjectTimingLogTestSuite) TestInject_TimingLogs() {
	logs := log.InitTestObserved(s.T(), zapcore.DebugLevel)

	ctx := context.Background()

	execFunc := func(_ context.Context, _ string, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
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
		return nil
	}

	wasExecuted, err := Inject(ctx, InjectOptions{
		Exec: execFunc,
		ScriptParams: &Params{
			Command:         "test-cmd",
			AgentRemotePath: testAgentRemotePath,
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

	s.Contains(messages, "start inject")
	s.True(
		containsPrefix(messages, "payload delivered elapsed="),
		"missing payload log: %v", messages,
	)
	s.True(
		containsPrefix(messages, "complete elapsed="),
		"missing complete log: %v", messages,
	)
	s.True(
		containsPrefix(messages, "handshake complete elapsed="),
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

func fakeInjectShHandshake(stdin io.Reader, stdout io.Writer) error {
	if _, err := stdout.Write([]byte("ping\n")); err != nil {
		return err
	}
	pong := make([]byte, 5)
	if _, err := io.ReadFull(stdin, pong); err != nil {
		return err
	}
	if string(pong) != testPong {
		return fmt.Errorf("expected pong, got %q", pong)
	}
	if _, err := stdout.Write([]byte("ARM-false\n")); err != nil {
		return err
	}
	return nil
}

func fakeInjectShReceiveBinary(stdin io.Reader) error {
	sizeLine, err := readDelimitedLine(stdin)
	if err != nil {
		return fmt.Errorf("read size: %w", err)
	}
	size, err := strconv.Atoi(strings.TrimSpace(sizeLine))
	if err != nil {
		return fmt.Errorf("parse size %q: %w", sizeLine, err)
	}
	binary := make([]byte, size)
	if _, err := io.ReadFull(stdin, binary); err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	if string(binary) != "BINARY-CONTENT" {
		return fmt.Errorf("binary mismatch: %q", binary)
	}
	return nil
}

func fakeInjectShBinaryTransfer(
	stdin io.Reader,
	stdout io.Writer,
	commandStarted chan<- struct{},
) error {
	if err := fakeInjectShHandshake(stdin, stdout); err != nil {
		return err
	}
	if err := fakeInjectShReceiveBinary(stdin); err != nil {
		return err
	}
	if _, err := stdout.Write([]byte("done\n")); err != nil {
		return err
	}
	commandStarted <- struct{}{}
	_, copyErr := io.Copy(stdout, stdin)
	return copyErr
}

func assertBridgeIsLive(t *testing.T, stdinW io.Writer, stdoutR io.Reader) {
	t.Helper()
	if _, err := stdinW.Write([]byte("hello-bridge")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 12)
	if _, err := io.ReadFull(stdoutR, got); err != nil {
		t.Fatalf("read echoed bridge bytes: %v", err)
	}
	if string(got) != "hello-bridge" {
		t.Fatalf("bridge echo mismatch: %q", got)
	}
}

func TestInject_BinaryInjectSingleExec(t *testing.T) {
	t.Parallel()

	var execCalls atomic.Int32
	commandStarted := make(chan struct{}, 1)
	execFunc := func(_ context.Context, _ string, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
		execCalls.Add(1)
		return fakeInjectShBinaryTransfer(stdin, stdout, commandStarted)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	injectDone := make(chan error, 1)
	go func() {
		_, err := Inject(context.Background(), InjectOptions{
			Exec: execFunc,
			LocalFile: func(_ bool) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("BINARY-CONTENT")), nil
			},
			ScriptParams: &Params{
				Command:             testCommand,
				AgentRemotePath:     testAgentRemotePath,
				DownloadURLs:        &DownloadURLs{},
				ExistsCheck:         testExistsCheck,
				PreferAgentDownload: false,
			},
			Stdin:   stdinR,
			Stdout:  stdoutW,
			Stderr:  io.Discard,
			Timeout: 5 * time.Second,
		})
		injectDone <- err
	}()

	select {
	case <-commandStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("command did not start on the single exec")
	}

	if got := execCalls.Load(); got != 1 {
		t.Fatalf("Exec called %d times, want 1 (single exec, no rerun)", got)
	}

	assertBridgeIsLive(t, stdinW, stdoutR)

	_ = stdinW.Close()
	_ = stdoutW.Close()

	select {
	case <-injectDone:
	case <-time.After(8 * time.Second):
		t.Fatal("Inject did not return after teardown")
	}
}

func TestResolveRunResult_ExecErrorSurvivesWhenBothChannelsAreReady(t *testing.T) {
	wantErr := errors.New("boom: exec failed after done")
	for i := range 200 {
		execErrChan := make(chan error, 1)
		execErrChan <- wantErr
		injectChan := make(chan injectResult, 1)
		injectChan <- injectResult{wasExecuted: true, err: nil}

		result, execErr := resolveRunResult(execErrChan, injectChan)

		require.True(t, result.wasExecuted, "iteration %d", i)
		require.NoError(t, result.err, "iteration %d", i)
		require.ErrorIs(t, execErr, wantErr, "iteration %d: exec error must not be dropped", i)
	}
}

func TestInject_HangsAfterHandshakeAbortsOnTimeout(t *testing.T) {
	t.Parallel()

	hanging := func(stdin io.Reader, stdout io.Writer) error {
		if _, err := stdout.Write([]byte("ping\n")); err != nil {
			return err
		}
		pong := make([]byte, 5)
		if _, err := io.ReadFull(stdin, pong); err != nil {
			return err
		}
		if string(pong) != testPong {
			return fmt.Errorf("expected pong, got %q", pong)
		}
		_, _ = io.Copy(io.Discard, stdin) //nolint:errcheck // test helper
		return nil
	}

	execFunc := func(_ context.Context, _ string, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
		return hanging(stdin, stdout)
	}

	const timeout = 400 * time.Millisecond
	start := time.Now()
	_, err := Inject(context.Background(), InjectOptions{
		Exec:      execFunc,
		LocalFile: func(bool) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
		ScriptParams: &Params{
			Command:         testCommand,
			AgentRemotePath: testAgentRemotePath,
			DownloadURLs:    &DownloadURLs{},
			ExistsCheck:     testExistsCheck,
		},
		Stdin:   strings.NewReader(""),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Timeout: timeout,
	})
	elapsed := time.Since(start)

	require.Error(t, err, "Inject must abort when the handshake stalls")
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"expected context.DeadlineExceeded, got %v", err)
	require.Less(t, int64(elapsed), int64(2*timeout),
		"Inject did not abort within timeout: elapsed=%s timeout=%s", elapsed, timeout)
}

func TestInject_PostHandshakeCommandSurvivesHandshakeTimeout(t *testing.T) {
	t.Parallel()

	const handshakeTimeout = 150 * time.Millisecond
	const bridgeDuration = 400 * time.Millisecond // longer than handshakeTimeout

	execFunc := func(ctx context.Context, _ string, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
		if _, err := stdout.Write([]byte("ping\n")); err != nil {
			return err
		}
		pong := make([]byte, 5)
		if _, err := io.ReadFull(stdin, pong); err != nil {
			return err
		}
		if string(pong) != testPong {
			return fmt.Errorf("expected pong, got %q", pong)
		}
		if _, err := stdout.Write([]byte("done\n")); err != nil {
			return err
		}
		select {
		case <-time.After(bridgeDuration):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	start := time.Now()
	_, err := Inject(context.Background(), InjectOptions{
		Exec:      execFunc,
		LocalFile: func(bool) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
		ScriptParams: &Params{
			Command:         testCommand,
			AgentRemotePath: testAgentRemotePath,
			DownloadURLs:    &DownloadURLs{},
			ExistsCheck:     testExistsCheck,
		},
		Stdin:   strings.NewReader(""),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Timeout: handshakeTimeout,
	})
	elapsed := time.Since(start)

	require.NoError(t, err,
		"a long-running post-handshake command must not be killed by the handshake timeout")
	require.GreaterOrEqual(t, int64(elapsed), int64(bridgeDuration),
		"Inject returned before the post-handshake command finished: elapsed=%s bridgeDuration=%s",
		elapsed, bridgeDuration)
}

func TestInject_RequiresExecAndScriptParams(t *testing.T) {
	t.Parallel()

	wasExecuted, err := Inject(context.Background(), InjectOptions{
		ScriptParams: &Params{Command: testCommand},
	})
	require.Error(t, err, "Exec is required")
	require.False(t, wasExecuted)

	wasExecuted, err = Inject(context.Background(), InjectOptions{
		Exec: func(context.Context, string, io.Reader, io.Writer, io.Writer) error { return nil },
	})
	require.Error(t, err, "ScriptParams is required")
	require.False(t, wasExecuted)
}

func TestInject_AlreadyCancelledContextReturnsAndClosesResources(t *testing.T) {
	t.Parallel()

	execRan := make(chan struct{}, 1)
	execFunc := func(_ context.Context, _ string, _ io.Reader, stdout io.Writer, _ io.Writer) error {
		execRan <- struct{}{}
		_, _ = stdout.Write([]byte("ping\n")) //nolint:errcheck
		<-time.After(time.Second)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	start := time.Now()
	_, err := Inject(ctx, InjectOptions{
		Exec:      execFunc,
		LocalFile: func(bool) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
		ScriptParams: &Params{
			Command:         testCommand,
			AgentRemotePath: testAgentRemotePath,
			DownloadURLs:    &DownloadURLs{},
			ExistsCheck:     testExistsCheck,
		},
		Stdin:   strings.NewReader(""),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Timeout: 5 * time.Second,
	})
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, int64(elapsed), int64(time.Second),
		"Inject did not return promptly on a pre-cancelled context: %s", elapsed)

	select {
	case <-execRan:
		t.Fatal("exec ran despite pre-cancelled context (resources not cleaned up before launch)")
	default:
	}
}

func readLineFatal(t *testing.T, r io.Reader) string {
	t.Helper()
	var line []byte
	for {
		b := make([]byte, 1)
		if _, err := io.ReadFull(r, b); err != nil {
			t.Fatalf("read line: %v", err)
		}
		if b[0] == '\n' {
			return strings.TrimRight(string(line), "\r")
		}
		line = append(line, b[0])
	}
}

func expectLine(t *testing.T, r io.Reader, want string) {
	t.Helper()
	if got := readLineFatal(t, r); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent Write (from the exec
// package's stderr-copying goroutine) and String (from the test goroutine),
// since cmd.Stderr may still be actively written to when the test reads it
// (e.g. on a timeout path where the process hasn't exited yet).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type shProcessIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr *syncBuffer
}

type shTestPipe struct {
	in  io.Writer
	out io.Reader
}

func startInjectShProcess(
	t *testing.T,
	command, installPath string,
	procIO shProcessIO,
) (*exec.Cmd, <-chan struct{}) {
	t.Helper()

	rendered, err := GenerateScript(Script, &Params{
		Command:             command,
		AgentRemotePath:     installPath,
		DownloadURLs:        &DownloadURLs{},
		ExistsCheck:         testExistsCheck,
		PreferAgentDownload: false,
		ShouldChmodPath:     true,
	})
	require.NoError(t, err)

	cmd := exec.Command( //nolint:gosec // G204: script content is test-generated, not external input
		"sh",
		"-c",
		rendered,
	)
	cmd.Stdin = procIO.stdin
	cmd.Stdout = procIO.stdout
	cmd.Stderr = procIO.stderr
	require.NoError(t, cmd.Start())

	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	return cmd, waitDone
}

func driveBinaryHandshake(t *testing.T, shPipe shTestPipe, binary []byte) {
	t.Helper()

	expectLine(t, shPipe.out, "ping")
	requireWrite(t, shPipe.in, testPong)
	if got := readLineFatal(t, shPipe.out); !strings.HasPrefix(got, "ARM-") {
		t.Fatalf("expected ARM-..., got %q", got)
	}
	requireWrite(t, shPipe.in, fmt.Sprintf("%d\n", len(binary)))
	requireWrite(t, shPipe.in, string(binary))
	expectLine(t, shPipe.out, "done")
}

func verifyBridgeLiveAndInstall(
	t *testing.T,
	shPipe shTestPipe,
	installPath string,
	binary []byte,
) {
	t.Helper()

	expectLine(t, shPipe.out, "CMD-START")
	requireWrite(t, shPipe.in, "bridge-traffic\n")
	if got := readLineFatal(t, shPipe.out); got != "bridge-traffic" {
		t.Fatalf("command stdin not live (head -c over-read?): got %q", got)
	}

	installed, err := os.ReadFile(installPath) //nolint:gosec // G304: path is test-controlled
	require.NoError(t, err)
	if !bytes.Equal(installed, binary) {
		t.Fatalf("installed binary mismatch: got %d bytes, want %d", len(installed), len(binary))
	}
}

func TestInjectScript_RealShellBinaryTransfer(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tmp := t.TempDir()
	installPath := filepath.Join(tmp, "bin", "agent")
	require.NoError(t, os.MkdirAll(filepath.Dir(installPath), 0o750))

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderr := &syncBuffer{}
	command := "printf 'CMD-START\\n' && cat"
	cmd, waitDone := startInjectShProcess(
		t,
		command,
		installPath,
		shProcessIO{stdin: stdinR, stdout: stdoutW, stderr: stderr},
	)
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = stdoutW.Close()
		_ = cmd.Process.Kill()
		<-waitDone
	})
	shPipe := shTestPipe{in: stdinW, out: stdoutR}

	binary := bytes.Repeat([]byte{0x07}, 4096) // arbitrary content incl. a 4KB chunk
	binary[0], binary[1], binary[len(binary)-1] = 'D', 'V', 'X'

	driveBinaryHandshake(t, shPipe, binary)
	verifyBridgeLiveAndInstall(t, shPipe, installPath, binary)

	_ = stdinW.Close()
	_ = stdoutW.Close()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("inject.sh did not exit: stderr=%s", stderr.String())
	}
}

func TestInjectScript_RealShellRejectsInvalidBinarySize(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tmp := t.TempDir()
	installPath := filepath.Join(tmp, "bin", "agent")
	require.NoError(t, os.MkdirAll(filepath.Dir(installPath), 0o750))

	rendered, err := GenerateScript(Script, &Params{
		Command:             "echo should-not-run",
		AgentRemotePath:     installPath,
		DownloadURLs:        &DownloadURLs{},
		ExistsCheck:         testExistsCheck,
		PreferAgentDownload: false,
		ShouldChmodPath:     true,
	})
	require.NoError(t, err)

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	cmd := exec.Command( //nolint:gosec // G204: script content is test-generated, not external input
		"sh",
		"-c",
		rendered,
	)
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	require.NoError(t, cmd.Start())
	waitDone := make(chan struct{})
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-waitDone
	})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	shPipe := shTestPipe{in: stdinW, out: stdoutR}
	expectLine(t, shPipe.out, "ping")
	requireWrite(t, shPipe.in, testPong)
	if got := readLineFatal(t, shPipe.out); !strings.HasPrefix(got, "ARM-") {
		t.Fatalf("expected ARM-..., got %q", got)
	}
	requireWrite(t, shPipe.in, "1K\n")

	if got := readLineFatal(t, stderrR); !strings.Contains(got, "Invalid binary size") {
		t.Fatalf("expected an invalid-size rejection on stderr, got %q", got)
	}

	_, statErr := os.Stat(installPath)
	require.True(t, os.IsNotExist(statErr), "invalid size must not install anything")

	_ = stdinW.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()
}

func requireWrite(t *testing.T, w io.Writer, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("write %q: %v", s, err)
	}
}

func readDelimitedLine(r io.Reader) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				return string(b), nil
			}
			b = append(b, one[0])
		}
		if err != nil {
			return string(b), err
		}
	}
}
