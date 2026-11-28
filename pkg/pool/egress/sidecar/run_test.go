//go:build !windows

package sidecar

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/devsy-org/devsy/pkg/pool/egress/channel"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/stretchr/testify/suite"
)

const (
	waitFor  = 5 * time.Second
	waitTick = 5 * time.Millisecond
	quiet    = 300 * time.Millisecond
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

func (k *sinkStub) denied() []egress.Denial {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]egress.Denial(nil), k.denials...)
}

type RunSuite struct {
	suite.Suite
	dir string
}

func TestRunSuite(t *testing.T) {
	suite.Run(t, new(RunSuite))
}

func (s *RunSuite) SetupTest() {
	dir, err := os.MkdirTemp("", "esc")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	s.dir = dir
}

type pool struct {
	path   string
	sink   *sinkStub
	server *channel.Server
	cancel context.CancelFunc
	done   chan error
}

type sidecarRun struct {
	cancel context.CancelFunc
	done   chan error
	addr   chan net.Addr
}

func (s *RunSuite) TestADecisionReachesThePoolAfterTheReadyLine() {
	p := s.startPool()
	run := s.startSidecar(s.config(p.path), Options{})
	addr := s.proxyAddr(run)
	s.waitReady(p)

	resp := s.connect(addr, "127.0.0.1:9")
	s.Equal(http.StatusForbidden, resp.StatusCode)
	s.Eventually(func() bool { return len(p.sink.denied()) == 1 }, waitFor, waitTick)
	got := p.sink.denied()[0]
	s.Equal("127.0.0.1", got.Host)
	s.Equal(9, got.Port)
}

func (s *RunSuite) TestTheProxyRefusesWith503WhileThePoolIsUnreachable() {
	cfg := s.config(filepath.Join(s.dir, "absent.sock"))
	run := s.startSidecar(cfg, Options{})
	addr := s.proxyAddr(run)

	resp := s.connect(addr, "127.0.0.1:9")
	s.Equal(http.StatusServiceUnavailable, resp.StatusCode)
}

func (s *RunSuite) TestTheProxyAcceptsConnectionsBeforeThePoolHearsReady() {
	p := s.startPool()
	var readyAtListen, accepted bool
	run := &sidecarRun{done: make(chan error, 1), addr: make(chan net.Addr, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	s.T().Cleanup(func() {
		cancel()
		<-run.done
	})
	opts := Options{Listening: func(a net.Addr) {
		select {
		case <-p.server.Ready():
			readyAtListen = true
		default:
		}
		conn, err := net.DialTimeout("tcp", a.String(), waitFor)
		accepted = err == nil
		if err == nil {
			_ = conn.Close()
		}
		run.addr <- a
	}}
	go func() { run.done <- Run(ctx, s.config(p.path), opts) }()
	s.proxyAddr(run)
	s.waitReady(p)
	s.False(readyAtListen, "the pool heard ready before the proxy was bound")
	s.True(accepted, "the proxy was not accepting connections when it was announced")
}

func (s *RunSuite) TestARunWithAWildcardBindDoesNotStart() {
	p := s.startPool()
	cfg := s.config(p.path)
	cfg.Bind = "0.0.0.0:0"
	s.ErrorContains(Run(context.Background(), cfg, Options{}), "bind")
	select {
	case <-p.server.Ready():
		s.Fail("the pool heard ready from a sidecar that never started")
	case <-time.After(quiet):
	}
}

type holdRecorder struct {
	egress.Recorder
	entered chan struct{}
	release chan struct{}
}

func (h *holdRecorder) Denied(d egress.Denial) {
	close(h.entered)
	<-h.release
	h.Recorder.Denied(d)
}

func (s *RunSuite) TestLoadRefusesAFIFOWithoutBlocking() {
	path := filepath.Join(s.dir, "egress.json")
	s.Require().NoError(syscall.Mkfifo(path, 0o600))
	done := make(chan error, 1)
	go func() {
		_, err := Load(path)
		done <- err
	}()
	select {
	case err := <-done:
		s.ErrorContains(err, "regular file")
	case <-time.After(waitFor):
		s.FailNow("Load blocked on a FIFO")
	}
}

func (s *RunSuite) TestStopDrainsADecisionInFlightBeforeTheChannelCloses() {
	p := s.startPool()
	hold := &holdRecorder{entered: make(chan struct{}), release: make(chan struct{})}
	opts := Options{WrapRecorder: func(r egress.Recorder) egress.Recorder {
		hold.Recorder = r
		return hold
	}}
	run := s.startSidecar(s.config(p.path), opts)
	addr := s.proxyAddr(run)
	s.waitReady(p)

	go func() {
		conn, err := net.DialTimeout("tcp", addr, waitFor)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("CONNECT 127.0.0.1:9 HTTP/1.1\r\nHost: 127.0.0.1:9\r\n\r\n"))
		_, _ = bufio.NewReader(conn).ReadByte()
	}()
	select {
	case <-hold.entered:
	case <-time.After(waitFor):
		s.FailNow("the decision never reached the recorder")
	}

	run.cancel()
	select {
	case <-run.done:
		s.FailNow("Run returned with a decision still in flight")
	case <-time.After(quiet):
	}
	close(hold.release)
	select {
	case err := <-run.done:
		s.NoError(err)
	case <-time.After(waitFor):
		s.FailNow("Run did not return after the decision finished")
	}
	s.Eventually(func() bool { return len(p.sink.denied()) == 1 }, waitFor, waitTick,
		"the in-flight decision was lost")
}

func (s *RunSuite) config(socket string) Config {
	return Config{
		Version:      ConfigVersion,
		SessionID:    testSession,
		Bind:         "127.0.0.1:0",
		EventsSocket: socket,
		Egress:       poolconfig.Egress{Default: poolconfig.EgressDeny},
		AlwaysDeny:   []string{testPoolNet},
	}
}

func (s *RunSuite) startPool() *pool {
	sink := &sinkStub{}
	srv, err := channel.NewServer(sink, nil)
	s.Require().NoError(err)
	path := filepath.Join(s.dir, "events.sock")
	ln, err := channel.Listen(path)
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(context.Background())
	p := &pool{path: path, sink: sink, server: srv, cancel: cancel, done: make(chan error, 1)}
	go func() { p.done <- srv.Serve(ctx, ln) }()
	s.T().Cleanup(func() {
		cancel()
		<-p.done
	})
	return p
}

func (s *RunSuite) startSidecar(cfg Config, opts Options) *sidecarRun {
	run := &sidecarRun{done: make(chan error, 1), addr: make(chan net.Addr, 1)}
	opts.Listening = func(a net.Addr) { run.addr <- a }
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	go func() { run.done <- Run(ctx, cfg, opts) }()
	s.T().Cleanup(func() {
		cancel()
		select {
		case <-run.done:
		case <-time.After(waitFor):
		}
	})
	return run
}

func (s *RunSuite) proxyAddr(run *sidecarRun) string {
	select {
	case a := <-run.addr:
		return a.String()
	case <-time.After(waitFor):
		s.FailNow("proxy never listened")
		return ""
	}
}

func (s *RunSuite) connect(addr, target string) *http.Response {
	conn, err := net.DialTimeout("tcp", addr, waitFor)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(waitFor))
	_, err = conn.Write([]byte("CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n"))
	s.Require().NoError(err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	s.Require().NoError(err)
	_ = resp.Body.Close()
	return resp
}

func (s *RunSuite) waitReady(p *pool) {
	select {
	case <-p.server.Ready():
	case <-time.After(waitFor):
		s.FailNow("pool never got the ready line")
	}
}
