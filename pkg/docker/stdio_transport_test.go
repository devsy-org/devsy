package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const helperModeEnv = "DEVSY_TEST_DIAL_STDIO_MODE"

// TestMain lets the test binary stand in for `docker system dial-stdio`.
func TestMain(m *testing.M) {
	switch os.Getenv(helperModeEnv) {
	case "":
		os.Exit(m.Run())
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "fail":
		_, _ = fmt.Fprintln(os.Stderr, "daemon unreachable")
		os.Exit(3)
	case "hang":
		time.Sleep(time.Minute)
	case "daemon":
		serveFakeDaemon()
	}
	os.Exit(0)
}

// serveFakeDaemon answers /_ping and /info over stdio; /info reports the
// DOCKER_HOST the subprocess was started with.
func serveFakeDaemon() {
	reader := bufio.NewReader(os.Stdin)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
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
			"HTTP/1.1 200 OK\r\nApi-Version: 1.41\r\n%sContent-Length: %d\r\n\r\n%s",
			header, len(body), body,
		)
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
	return &DockerInvocation{DockerCmd: exe, Env: env}
}

func (s *StdioTransportSuite) TestRoundTripAndHalfClose() {
	conn, err := NewStdioTransport(helperInvocation(s.T(), "echo")).Dial(context.Background())
	s.Require().NoError(err)
	defer func() { _ = conn.Close() }()

	_, err = conn.Write([]byte("ping"))
	s.Require().NoError(err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	s.Require().NoError(err)
	s.Equal("ping", string(buf))

	s.Require().NoError(conn.(interface{ CloseWrite() error }).CloseWrite())
	_, err = conn.Read(buf)
	s.ErrorIs(err, io.EOF, "echo exits cleanly once stdin is closed")
}

func (s *StdioTransportSuite) TestSubprocessFailureSurfacesStderr() {
	conn, err := NewStdioTransport(helperInvocation(s.T(), "fail")).Dial(context.Background())
	s.Require().NoError(err)
	defer func() { _ = conn.Close() }()

	_, err = conn.Read(make([]byte, 1))
	s.Require().Error(err)
	s.Contains(err.Error(), "daemon unreachable")
	s.Contains(err.Error(), "exit status 3")
}

func (s *StdioTransportSuite) TestMissingCommandIsActionable() {
	_, err := NewStdioTransport(&DockerInvocation{
		DockerCmd: "devsy-no-such-docker-binary",
		Env:       map[string]string{},
	}).Dial(context.Background())
	s.Require().Error(err)
	s.Contains(err.Error(), "devsy-no-such-docker-binary")
}

func (s *StdioTransportSuite) TestCancelledContextDoesNotStartProcess() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewStdioTransport(helperInvocation(s.T(), "echo")).Dial(ctx)
	s.ErrorIs(err, context.Canceled)
}

func (s *StdioTransportSuite) TestCloseKillsUnresponsiveSubprocess() {
	transport := NewStdioTransport(helperInvocation(s.T(), "hang"))
	transport.ShutdownTimeout = 100 * time.Millisecond
	conn, err := transport.Dial(context.Background())
	s.Require().NoError(err)

	start := time.Now()
	s.Require().NoError(conn.Close())
	s.Less(time.Since(start), 10*time.Second)
	s.Require().NoError(conn.Close(), "Close is idempotent")
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

func (s *StdioTransportSuite) TestClientPingsAndUsesInvocationEnv() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClientWithInvocation(
		ctx,
		helperInvocation(s.T(), "daemon", "DOCKER_HOST=ssh://provider-host"),
	)
	s.Require().NoError(err)
	defer func() { _ = client.Close() }()

	_, err = client.Ping(ctx)
	s.Require().NoError(err)

	info, err := client.Info(ctx)
	s.Require().NoError(err)
	s.Equal("ssh://provider-host", info.Name)
}

func (s *StdioTransportSuite) TestClientRejectsEmptyInvocation() {
	_, err := NewClientWithInvocation(context.Background(), &DockerInvocation{})
	s.Error(err)
}
