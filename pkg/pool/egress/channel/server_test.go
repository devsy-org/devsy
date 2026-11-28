//go:build !windows

package channel

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	waitFor  = 5 * time.Second
	waitTick = 5 * time.Millisecond
)

type sinkStub struct {
	mu      sync.Mutex
	denials []egress.Denial
	allows  []egress.Allow
}

func (k *sinkStub) Denied(d egress.Denial) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.denials = append(k.denials, d)
}

func (k *sinkStub) Allowed(a egress.Allow) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.allows = append(k.allows, a)
}

func (k *sinkStub) counts() (int, int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.denials), len(k.allows)
}

type serverEnv struct {
	path   string
	sink   *sinkStub
	server *Server
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

type ServerSuite struct {
	suite.Suite
	dir string
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

func (s *ServerSuite) SetupTest() {
	s.dir = shortDir(s.T())
}

func (s *ServerSuite) TestNewServerNeedsASink() {
	_, err := NewServer(nil, nil)
	s.Error(err)
}

func (s *ServerSuite) TestListenCreatesAPrivateSocketAndNeverReplacesAPath() {
	path := filepath.Join(s.dir, "a.sock")
	ln, err := Listen(path)
	s.Require().NoError(err)
	defer func() { _ = ln.Close() }()
	info, err := os.Lstat(path)
	s.Require().NoError(err)
	s.Equal(os.ModeSocket|socketMode, info.Mode())

	_, err = Listen(path)
	s.Error(err, "a live socket")

	file := filepath.Join(s.dir, "file")
	s.Require().NoError(os.WriteFile(file, []byte("keep"), 0o600))
	_, err = Listen(file)
	s.ErrorContains(err, "already exists", "a stale file")
	kept, err := os.Stat(file)
	s.Require().NoError(err)
	s.Equal(int64(len("keep")), kept.Size())

	room := maxSocketPath - len(s.dir) - 1
	ok, err := Listen(filepath.Join(s.dir, strings.Repeat("b", room)))
	s.Require().NoError(err, "a path at the limit")
	_ = ok.Close()
	_, err = Listen(filepath.Join(s.dir, strings.Repeat("b", room+1)))
	s.ErrorContains(err, "longer than", "a path over the limit")
}

func (s *ServerSuite) TestListenRefusesADirectoryOthersCanReach() {
	open := filepath.Join(s.dir, "open")
	s.Require().NoError(os.Mkdir(open, 0o750))
	_, err := Listen(filepath.Join(open, "a.sock"))
	s.ErrorIs(err, errParentNotPrivate)
	_, err = Listen(filepath.Join(s.dir, "missing", "a.sock"))
	s.ErrorIs(err, errParentNotPrivate)
}

func (s *ServerSuite) TestIdleConnectionsAreClosedAndTheirSlotsReused() {
	path := filepath.Join(s.dir, "a.sock")
	sink := &sinkStub{}
	srv, err := NewServer(sink, nil)
	s.Require().NoError(err)
	srv.idle = 100 * time.Millisecond
	ln, err := Listen(path)
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	s.T().Cleanup(func() {
		cancel()
		<-done
	})

	var idle []net.Conn
	for range maxConns {
		c, err := net.Dial("unix", path)
		s.Require().NoError(err)
		s.T().Cleanup(func() { _ = c.Close() })
		idle = append(idle, c)
	}
	for _, c := range idle {
		s.Require().NoError(c.SetReadDeadline(time.Now().Add(waitFor)))
		_, err := c.Read(make([]byte, 1))
		s.ErrorIs(err, io.EOF, "the server closes an idle connection")
	}
	c, err := net.Dial("unix", path)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = c.Close() })
	_, err = c.Write([]byte(`{"t":"allow","host":"a.example.com","port":443}` + "\n"))
	s.Require().NoError(err)
	s.Eventually(func() bool {
		_, a := sink.counts()
		return a == 1
	}, waitFor, waitTick)
	s.Zero(srv.Stats().Refused)
}

func (s *ServerSuite) TestEventsReachTheSinkAndReadyIsReported() {
	env := s.start()
	conn := s.dial(env)
	select {
	case <-env.server.Ready():
		s.Fail("ready before the sidecar said so")
	default:
	}
	s.write(conn, `{"t":"ready"}`+"\n")
	s.write(conn, `{"t":"deny","host":"a.example.com","port":443,"reason":"not_allowed"}`+"\n")
	s.write(conn, `{"t":"allow","host":"b.example.com","port":443,"rule":1}`+"\n")
	s.waitCounts(env, 1, 1)
	select {
	case <-env.server.Ready():
	case <-time.After(waitFor):
		s.Fail("ready was not reported")
	}
	s.Equal("a.example.com", env.sink.denials[0].Host)
	s.Equal(egress.NotAllowed, env.sink.denials[0].Reason)
	s.Equal(egress.RuleAt(1), env.sink.allows[0].Rule)
	s.Equal(ServerStats{Events: 2}, env.server.Stats())
}

func (s *ServerSuite) TestBadLinesAreCountedAndTheStreamGoesOn() {
	env := s.start()
	conn := s.dial(env)
	huge := strings.Repeat("a", 5*MaxLine)
	s.write(conn, "garbage\n")
	s.write(conn, `{"t":"allow","host":"a.example.com","port":443,"session":"x"}`+"\n")
	s.write(conn, huge+"\n")
	s.write(conn, `{"t":"allow","host":"c.example.com","port":443}`+"\n")
	s.waitCounts(env, 0, 1)
	st := env.server.Stats()
	s.Equal(2, st.Invalid)
	s.Equal(1, st.Oversized)
	s.Equal(1, st.Events)
}

func (s *ServerSuite) TestALineWithoutANewlineIsNeverDelivered() {
	env := s.start()
	conn := s.dial(env)
	s.write(conn, `{"t":"allow","host":"a.example.com","port":443}`)
	s.Require().NoError(conn.Close())
	time.Sleep(50 * time.Millisecond)
	d, a := env.sink.counts()
	s.Zero(d + a)
}

func (s *ServerSuite) TestOnlyMaxConnsReadersRunAtOnce() {
	env := s.start()
	var conns []net.Conn
	for range maxConns {
		conns = append(conns, s.dial(env))
	}
	for _, c := range conns {
		s.write(c, `{"t":"ready"}`+"\n")
	}
	extra := s.dial(env)
	s.Eventually(func() bool { return env.server.Stats().Refused == 1 }, waitFor, waitTick)
	s.Require().NoError(extra.SetReadDeadline(time.Now().Add(waitFor)))
	_, err := extra.Read(make([]byte, 1))
	s.Error(err, "the refused connection is closed")

	s.Require().NoError(conns[0].Close())
	s.Eventually(func() bool {
		c, err := net.Dial("unix", env.path)
		if err != nil {
			return false
		}
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte(`{"t":"allow","host":"d.example.com","port":443}` + "\n"))
		return err == nil && func() bool {
			time.Sleep(20 * time.Millisecond)
			_, a := env.sink.counts()
			return a == 1
		}()
	}, waitFor, 50*time.Millisecond, "a freed slot is reused")
}

func (s *ServerSuite) TestStopDeliversWhatWasAlreadySentAndEndsIdleReaders() {
	env := s.start()
	conn := s.dial(env)
	for range 50 {
		s.write(conn, `{"t":"allow","host":"a.example.com","port":443}`+"\n")
	}
	start := time.Now()
	stopped := make(chan struct{})
	go func() {
		env.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(waitFor):
		s.Fail("Serve did not return with an idle connection open")
	}
	s.Less(time.Since(start), drainWait+time.Second)
	_, a := env.sink.counts()
	s.Equal(50, a)
}

func (s *ServerSuite) start() *serverEnv {
	return startServer(s.T(), filepath.Join(s.dir, "a.sock"))
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "egc")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func startServer(t *testing.T, path string) *serverEnv {
	t.Helper()
	sink := &sinkStub{}
	env := startServerFor(t, path, sink)
	env.sink = sink
	return env
}

func startServerFor(t *testing.T, path string, sink Sink) *serverEnv {
	t.Helper()
	srv, err := NewServer(sink, nil)
	require.NoError(t, err)
	ln, err := Listen(path)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	env := &serverEnv{
		path: path, server: srv, cancel: cancel,
		done: make(chan error, 1),
	}
	go func() { env.done <- srv.Serve(ctx, ln) }()
	t.Cleanup(env.stop)
	return env
}

// stop ends the server and waits for it. It is safe to call twice.
func (e *serverEnv) stop() {
	e.once.Do(func() {
		e.cancel()
		<-e.done
	})
}

func (s *ServerSuite) dial(env *serverEnv) net.Conn {
	conn, err := net.Dial("unix", env.path)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = conn.Close() })
	return conn
}

func (s *ServerSuite) write(conn net.Conn, line string) {
	_, err := conn.Write([]byte(line))
	s.Require().NoError(err)
}

func (s *ServerSuite) waitCounts(env *serverEnv, denials, allows int) {
	s.Eventually(func() bool {
		d, a := env.sink.counts()
		return d == denials && a == allows
	}, waitFor, waitTick)
}
