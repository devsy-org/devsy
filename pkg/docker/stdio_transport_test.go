package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	helperModeEnv   = "DEVSY_TEST_DIAL_STDIO_MODE"
	shortShutdown   = 150 * time.Millisecond
	boundedInterval = 5 * time.Second
)

var helperModes = map[string]func(){
	"echo": func() { _, _ = io.Copy(os.Stdout, os.Stdin) },
	"fail": func() {
		_, _ = fmt.Fprintln(os.Stderr, "daemon unreachable")
		os.Exit(3)
	},
	"fail-secret": func() {
		_, _ = fmt.Fprintln(os.Stderr, "auth failed for hunter2-token")
		os.Exit(4)
	},
	"hang": func() { time.Sleep(time.Minute) },
	"lingering": func() {
		_ = os.Stdout.Close()
		time.Sleep(time.Minute)
	},
	"spawn-child": spawnHangingChild,
	"daemon":      serveFakeDaemon,
}

func TestMain(m *testing.M) {
	mode := os.Getenv(helperModeEnv)
	if mode == "" {
		os.Exit(m.Run())
	}
	if run, ok := helperModes[mode]; ok {
		run()
	}
	os.Exit(0)
}

// spawnHangingChild starts a long-lived descendant immediately, reports its
// pid on stdout and then hangs, standing in for docker.exe starting ssh.exe.
func spawnHangingChild() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	//nolint:gosec // re-executes this test binary
	cmd := exec.Command(exe)
	cmd.Env = append(envWithout(os.Environ(), helperModeEnv), helperModeEnv+"=hang")
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "start descendant:", err)
		os.Exit(2)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%d\n", cmd.Process.Pid)
	time.Sleep(time.Minute)
}

func envWithout(environ []string, key string) []string {
	prefix := strings.ToUpper(key) + "="
	kept := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(strings.ToUpper(kv), prefix) {
			kept = append(kept, kv)
		}
	}
	return kept
}

func serveFakeDaemon() {
	reader := bufio.NewReader(os.Stdin)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		switch {
		case strings.HasSuffix(req.URL.Path, "/stuck"):
			time.Sleep(time.Minute)
			return
		case strings.HasSuffix(req.URL.Path, "/session"):
			_, _ = io.WriteString(
				os.Stdout,
				"HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n",
			)
			_, _ = io.Copy(os.Stdout, reader)
			return
		}

		body := "OK"
		header := "Content-Type: text/plain\r\n"
		if strings.HasSuffix(req.URL.Path, "/info") {
			data, _ := json.Marshal(map[string]string{"Name": os.Getenv("DOCKER_HOST")})
			body = string(data)
			header = "Content-Type: application/json\r\n"
		}
		_, _ = fmt.Fprintf(
			os.Stdout,
			"HTTP/1.1 200 OK\r\nApi-Version: 1.41\r\n%sContent-Length: %d\r\n\r\n",
			header, len(body),
		)
		if req.Method != http.MethodHead {
			_, _ = io.WriteString(os.Stdout, body)
		}
	}
}

type StdioTransportSuite struct {
	suite.Suite
}

func TestStdioTransportSuite(t *testing.T) {
	suite.Run(t, new(StdioTransportSuite))
}

func helperInvocation(t *testing.T, mode string, extra ...string) *DockerInvocation {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	env := map[string]string{helperModeEnv: mode}
	for _, kv := range extra {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return &DockerInvocation{DockerCmd: exe, env: env}
}

func dial(s *StdioTransportSuite, mode string, extra ...string) (*StdioTransport, *stdioConn) {
	transport := NewStdioTransport(helperInvocation(s.T(), mode, extra...))
	transport.ShutdownTimeout = shortShutdown
	conn, err := transport.Dial(context.Background())
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = conn.Close() })
	return transport, conn.(*stdioConn)
}

func assertTimeout(s *StdioTransportSuite, err error) {
	s.Require().Error(err)
	s.ErrorIs(err, os.ErrDeadlineExceeded)
	var netErr net.Error
	s.Require().ErrorAs(err, &netErr)
	s.True(netErr.Timeout())
}

func openConns(t *StdioTransport) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.conns)
}

func (s *StdioTransportSuite) TestRoundTripAndHalfClose() {
	_, conn := dial(s, "echo")

	_, err := conn.Write([]byte("ping"))
	s.Require().NoError(err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	s.Require().NoError(err)
	s.Equal("ping", string(buf))

	s.Require().NoError(conn.CloseWrite())
	_, err = conn.Read(buf)
	s.ErrorIs(err, io.EOF, "echo exits cleanly once stdin is closed")
}

func (s *StdioTransportSuite) TestCloseWriteStillDrainsFinalResponse() {
	_, conn := dial(s, "echo")

	_, err := conn.Write([]byte("last words"))
	s.Require().NoError(err)
	s.Require().NoError(conn.CloseWrite())

	got, err := io.ReadAll(conn)
	s.Require().NoError(err)
	s.Equal("last words", string(got))

	_, err = conn.Write([]byte("more"))
	s.Error(err, "write after CloseWrite must fail")
}

func (s *StdioTransportSuite) TestStreamsMoreThanOnePipeBuffer() {
	_, conn := dial(s, "echo")
	payload := bytes.Repeat([]byte("0123456789abcdef"), 1<<18) // 4 MiB

	var wg sync.WaitGroup
	wg.Add(1)
	var writeErr error
	go func() {
		defer wg.Done()
		_, writeErr = conn.Write(payload)
		_ = conn.CloseWrite()
	}()
	got, err := io.ReadAll(conn)
	wg.Wait()

	s.Require().NoError(writeErr)
	s.Require().NoError(err)
	s.True(bytes.Equal(payload, got))
}

func (s *StdioTransportSuite) TestReadDeadlineExpiresAndCanBeReset() {
	_, conn := dial(s, "echo")

	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)))
	_, err := conn.Read(make([]byte, 1))
	assertTimeout(s, err)

	s.Require().NoError(conn.SetReadDeadline(time.Time{}))
	_, err = conn.Write([]byte("x"))
	s.Require().NoError(err)
	buf := make([]byte, 1)
	_, err = io.ReadFull(conn, buf)
	s.Require().NoError(err, "a zero time clears the deadline")
}

func (s *StdioTransportSuite) TestWriteDeadlineExpiresWhenChildStopsReading() {
	_, conn := dial(s, "hang")

	s.Require().NoError(conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)))
	_, err := conn.Write(bytes.Repeat([]byte("x"), 16<<20))
	assertTimeout(s, err)
}

func (s *StdioTransportSuite) TestCombinedDeadlineCoversBothDirections() {
	_, conn := dial(s, "hang")
	s.Require().NoError(conn.SetDeadline(time.Now().Add(100 * time.Millisecond)))

	_, err := conn.Read(make([]byte, 1))
	assertTimeout(s, err)
	_, err = conn.Write(bytes.Repeat([]byte("x"), 16<<20))
	assertTimeout(s, err)
}

func (s *StdioTransportSuite) TestReadAndWriteDeadlinesAreIndependent() {
	_, conn := dial(s, "echo")
	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(-time.Second)))

	_, err := conn.Write([]byte("still writable"))
	s.Require().NoError(err)
	_, err = conn.Read(make([]byte, 1))
	assertTimeout(s, err)
}

func (s *StdioTransportSuite) TestCloseUnblocksBlockedReadAndWrite() {
	_, conn := dial(s, "hang")

	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		readDone <- err
	}()
	go func() {
		_, err := conn.Write(bytes.Repeat([]byte("x"), 16<<20))
		writeDone <- err
	}()
	time.Sleep(100 * time.Millisecond)

	s.Require().NoError(conn.Close())
	for _, done := range []chan error{readDone, writeDone} {
		select {
		case err := <-done:
			s.Require().Error(err)
		case <-time.After(boundedInterval):
			s.Fail("blocked I/O was not released by Close")
		}
	}
	s.Require().NoError(conn.Close(), "Close is idempotent")
}

func (s *StdioTransportSuite) TestConcurrentCloseIsSafe() {
	_, conn := dial(s, "echo")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(3)
		go func() { defer wg.Done(); _ = conn.Close() }()
		go func() { defer wg.Done(); _ = conn.CloseWrite() }()
		go func() { defer wg.Done(); _, _ = conn.Read(make([]byte, 1)) }()
	}
	wg.Wait()
}

func (s *StdioTransportSuite) TestStdoutEOFFromLiveChildCannotBlockRead() {
	transport, conn := dial(s, "lingering")

	start := time.Now()
	_, err := conn.Read(make([]byte, 1))
	s.Require().Error(err)
	s.Less(time.Since(start), boundedInterval)

	s.Require().NoError(conn.Close())
	select {
	case <-conn.exited:
	default:
		s.Fail("child was not reaped after forced teardown")
	}
	s.Zero(openConns(transport))
}

func (s *StdioTransportSuite) TestSubprocessFailureSurfacesStderr() {
	_, conn := dial(s, "fail")

	_, err := conn.Read(make([]byte, 1))
	s.Require().Error(err)
	s.Contains(err.Error(), "daemon unreachable")
	s.Contains(err.Error(), "exit status 3")
}

func (s *StdioTransportSuite) TestStderrTailIsRedacted() {
	_, conn := dial(s, "fail-secret", "DOCKER_PASSWORD=hunter2-token")

	_, err := conn.Read(make([]byte, 1))
	s.Require().Error(err)
	s.Contains(err.Error(), "auth failed")
	s.NotContains(err.Error(), "hunter2-token")
}

func (s *StdioTransportSuite) TestMissingCommandIsActionable() {
	_, err := NewStdioTransport(&DockerInvocation{
		DockerCmd: "devsy-no-such-docker-binary",
		env:       map[string]string{},
	}).Dial(context.Background())
	s.Require().Error(err)
	s.Contains(err.Error(), "devsy-no-such-docker-binary")
}

func (s *StdioTransportSuite) TestCancelledContextDoesNotStartProcess() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := NewStdioTransport(helperInvocation(s.T(), "echo"))
	_, err := transport.Dial(ctx)
	s.ErrorIs(err, context.Canceled)
	s.Zero(openConns(transport))
}

func (s *StdioTransportSuite) TestCloseKillsUnresponsiveSubprocess() {
	transport, conn := dial(s, "hang")

	start := time.Now()
	s.Require().NoError(conn.Close())
	s.Less(time.Since(start), boundedInterval)
	s.Require().NoError(conn.Close(), "Close is idempotent")
	s.Zero(openConns(transport))
}

func (s *StdioTransportSuite) TestRepeatedDialsDoNotLeakGoroutines() {
	transport := NewStdioTransport(helperInvocation(s.T(), "echo"))
	transport.ShutdownTimeout = shortShutdown
	baseline := runtime.NumGoroutine()

	for range 20 {
		conn, err := transport.Dial(context.Background())
		s.Require().NoError(err)
		_, err = conn.Write([]byte("x"))
		s.Require().NoError(err)
		s.Require().NoError(conn.Close())
	}

	s.Eventually(func() bool {
		return runtime.NumGoroutine() <= baseline+2
	}, boundedInterval, 25*time.Millisecond, "goroutines accumulated across dials")
	s.Zero(openConns(transport))
}

func (s *StdioTransportSuite) TestDialedConnOutlivesDialContext() {
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := NewStdioTransport(helperInvocation(s.T(), "echo")).Dial(ctx)
	s.Require().NoError(err)
	defer func() { _ = conn.Close() }()
	cancel()

	_, err = conn.Write([]byte("x"))
	s.Require().NoError(err)
	buf := make([]byte, 1)
	_, err = io.ReadFull(conn, buf)
	s.Require().NoError(err)
}

func (s *StdioTransportSuite) TestTransportCloseReleasesOwnedConnections() {
	transport, conn := dial(s, "echo")

	s.Require().NoError(transport.Close())
	_, err := conn.Read(make([]byte, 1))
	s.Error(err)
	s.Zero(openConns(transport))
}

func newDaemonClient(s *StdioTransportSuite) *Client {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewClientWithInvocation(
		ctx,
		helperInvocation(s.T(), "daemon", "DOCKER_HOST=ssh://provider-host"),
	)
	s.Require().NoError(err)
	client.stdio.ShutdownTimeout = shortShutdown
	s.T().Cleanup(func() { _ = client.Close() })
	return client
}

func (s *StdioTransportSuite) TestClientPingsAndUsesInvocationEnv() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := newDaemonClient(s)

	_, err := client.Ping(ctx)
	s.Require().NoError(err)

	info, err := client.Info(ctx)
	s.Require().NoError(err)
	s.Equal("ssh://provider-host", info.Name)
}

func (s *StdioTransportSuite) TestClientRejectsEmptyInvocation() {
	_, err := NewClientWithInvocation(context.Background(), &DockerInvocation{})
	s.Error(err)
}

func echoThrough(s *StdioTransportSuite, conn net.Conn, msg string) {
	_, err := conn.Write([]byte(msg))
	s.Require().NoError(err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(conn, buf)
	s.Require().NoError(err)
	s.Equal(msg, string(buf))
}

func (s *StdioTransportSuite) TestCancelledUpgradeReturnsPromptlyWithoutDisturbingOthers() {
	client := newDaemonClient(s)

	healthy, err := client.DialHijack(context.Background(), "/session", "tcp", nil)
	s.Require().NoError(err)
	defer func() { _ = healthy.Close() }()
	echoThrough(s, healthy, "before")
	baseline := openConns(client.stdio)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err = client.DialHijack(ctx, "/stuck", "tcp", nil)
	s.Require().Error(err)
	s.ErrorIs(err, context.Canceled)
	s.Less(time.Since(start), boundedInterval)

	echoThrough(s, healthy, "after")
	s.Eventually(func() bool { return openConns(client.stdio) == baseline },
		boundedInterval, 25*time.Millisecond, "aborted attempt's child must be cleaned up")
}

func (s *StdioTransportSuite) TestCancelBeforeUpgradeStartsDoesNotSpawn() {
	client := newDaemonClient(s)
	before := openConns(client.stdio)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.DialHijack(ctx, "/session", "tcp", nil)
	s.ErrorIs(err, context.Canceled)
	s.Equal(before, openConns(client.stdio))
}

func (s *StdioTransportSuite) TestCancelAfterSuccessfulUpgradeKeepsConnection() {
	client := newDaemonClient(s)

	ctx, cancel := context.WithCancel(context.Background())
	conn, err := client.DialHijack(ctx, "/session", "tcp", nil)
	s.Require().NoError(err)
	defer func() { _ = conn.Close() }()

	cancel()
	time.Sleep(100 * time.Millisecond)
	echoThrough(s, conn, "still alive")
}

func (s *StdioTransportSuite) TestClientCloseReleasesHijackedConnections() {
	client := newDaemonClient(s)

	conn, err := client.DialHijack(context.Background(), "/session", "tcp", nil)
	s.Require().NoError(err)
	echoThrough(s, conn, "hi")

	s.Require().NoError(client.Close())
	_, err = conn.Read(make([]byte, 1))
	s.Require().Error(err)
	s.Zero(openConns(client.stdio))
}
