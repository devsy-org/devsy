//go:build !windows

package channel

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/suite"
)

const fastRetry = 20 * time.Millisecond

var (
	senderDeny = egress.Denial{
		Target: egress.Target{Host: "a.example.com", Port: 443},
		Reason: egress.NotAllowed,
	}
	senderAllow = egress.Allow{
		Target: egress.Target{Host: "b.example.com", Port: 443},
		Rule:   egress.RuleAt(0),
	}
)

type SenderSuite struct {
	suite.Suite
	dir string
}

func TestSenderSuite(t *testing.T) {
	suite.Run(t, new(SenderSuite))
}

func (s *SenderSuite) SetupTest() {
	s.dir = shortDir(s.T())
}

func (s *SenderSuite) TestNewSenderChecksItsConfig() {
	_, err := NewSender(SenderConfig{})
	s.Error(err)
	_, err = NewSender(SenderConfig{Path: "/x", Retry: -time.Second})
	s.Error(err)
	sender, err := NewSender(SenderConfig{Path: "/x"})
	s.Require().NoError(err)
	s.Equal(defaultRetry, sender.cfg.Retry)
}

func (s *SenderSuite) TestASenderIsNotHealthyUntilItHasConnected() {
	sender := s.newSender(filepath.Join(s.dir, "none.sock"))
	s.False(sender.Healthy())
	sender.Denied(senderDeny)
	sender.Allowed(senderAllow)
	s.Equal(SenderStats{Dropped: 2}, sender.Stats())
}

func (s *SenderSuite) TestRunConnectsAnnouncesReadyAndDeliversDecisions() {
	path := filepath.Join(s.dir, "a.sock")
	env := startServer(s.T(), path)
	sender := s.runSender(path)
	s.waitHealthy(sender, true)
	select {
	case <-env.server.Ready():
	case <-time.After(waitFor):
		s.Fail("the pool never saw ready")
	}
	sender.Denied(senderDeny)
	sender.Allowed(senderAllow)
	s.Eventually(func() bool {
		d, a := env.sink.counts()
		return d == 1 && a == 1
	}, waitFor, waitTick)
	env.sink.mu.Lock()
	defer env.sink.mu.Unlock()
	s.Equal(senderDeny, env.sink.denials[0])
	s.Equal(senderAllow, env.sink.allows[0])
	s.Equal(SenderStats{Sent: 2}, sender.Stats())
}

func (s *SenderSuite) TestADecisionThatCannotBeEncodedIsDroppedButStaysHealthy() {
	path := filepath.Join(s.dir, "a.sock")
	startServer(s.T(), path)
	sender := s.runSender(path)
	s.waitHealthy(sender, true)
	sender.Denied(egress.Denial{Target: senderDeny.Target, Reason: "made_up"})
	s.Equal(SenderStats{Dropped: 1}, sender.Stats())
	s.True(sender.Healthy())
}

func (s *SenderSuite) TestOneSendToAnUnreachablePoolMakesItUnhealthy() {
	path := filepath.Join(s.dir, "a.sock")
	env := startServer(s.T(), path)
	sender := s.runSender(path)
	s.waitHealthy(sender, true)
	env.stop()

	sender.Allowed(senderAllow)
	s.False(sender.Healthy(), "the first lost decision must stop the proxy serving")
	s.Equal(SenderStats{Dropped: 1}, sender.Stats())
}

func (s *SenderSuite) TestAStaleConnectionIsReplacedAndTheLineIsResent() {
	path := filepath.Join(s.dir, "a.sock")
	first := startServer(s.T(), path)
	sender := s.runSender(path)
	s.waitHealthy(sender, true)
	first.stop()
	second := startServer(s.T(), path)

	sender.Denied(senderDeny)
	s.Eventually(func() bool {
		d, _ := second.sink.counts()
		return d == 1
	}, waitFor, waitTick, "the new pool receives the line")
	s.Equal(SenderStats{Sent: 1}, sender.Stats())
	s.True(sender.Healthy())
	select {
	case <-second.server.Ready():
	case <-time.After(waitFor):
		s.Fail("the new pool never saw ready")
	}
}

// failConn is a connection to a pool that accepts the dial but cannot take a
// write, which is a failure that does not mean the pool is gone.
type failConn struct {
	net.Conn
	fail *atomic.Bool
}

func (c failConn) Write(p []byte) (int, error) {
	if c.fail.Load() {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

func (failConn) SetWriteDeadline(time.Time) error { return nil }
func (failConn) Close() error                     { return nil }

func failingSender(s *SenderSuite, connected, redialWorks bool) *Sender {
	var broken, working atomic.Bool
	broken.Store(true)
	sender := s.newSender(filepath.Join(s.dir, "unused.sock"))
	sender.dialer = func(context.Context) (net.Conn, error) {
		if redialWorks {
			return failConn{fail: &working}, nil
		}
		return failConn{fail: &broken}, nil
	}
	if connected {
		sender.conn = failConn{fail: &broken}
	}
	sender.healthy.Store(true)
	return sender
}

func (s *SenderSuite) TestOneWriteFailureOnAFreshConnectionMakesItUnhealthy() {
	sender := failingSender(s, false, false)
	sender.Denied(senderDeny)
	s.False(sender.Healthy(), "a dropped decision must stop the proxy serving at once")
	s.Equal(SenderStats{Dropped: 1}, sender.Stats())
}

func (s *SenderSuite) TestAFailedResendMakesItUnhealthy() {
	sender := failingSender(s, true, false)
	sender.Allowed(senderAllow)
	s.False(sender.Healthy())
	s.Equal(SenderStats{Dropped: 1}, sender.Stats())
}

func (s *SenderSuite) TestAResendOnANewConnectionKeepsItHealthy() {
	sender := failingSender(s, true, true)
	sender.Denied(senderDeny)
	s.True(sender.Healthy())
	s.Equal(SenderStats{Sent: 1}, sender.Stats())
}

func (s *SenderSuite) TestRunBringsItBackWhenThePoolReturns() {
	path := filepath.Join(s.dir, "a.sock")
	sender := s.runSender(path)
	time.Sleep(3 * fastRetry)
	s.False(sender.Healthy(), "no pool yet")

	env := startServer(s.T(), path)
	s.waitHealthy(sender, true)
	sender.Allowed(senderAllow)
	s.Eventually(func() bool {
		_, a := env.sink.counts()
		return a == 1
	}, waitFor, waitTick)

	env.stop()
	sender.Allowed(senderAllow)
	s.waitHealthy(sender, false)
	again := startServer(s.T(), path)
	s.waitHealthy(sender, true)
	select {
	case <-again.server.Ready():
	case <-time.After(waitFor):
		s.Fail("the new pool never saw ready")
	}
}

func (s *SenderSuite) TestRunEndsAndClosesOnCancel() {
	path := filepath.Join(s.dir, "a.sock")
	startServer(s.T(), path)
	sender := s.newSender(path)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sender.Run(ctx) }()
	s.waitHealthy(sender, true)
	cancel()
	select {
	case err := <-done:
		s.NoError(err)
	case <-time.After(waitFor):
		s.Fail("Run did not end")
	}
	s.False(sender.Healthy())
	sender.Denied(senderDeny)
	s.Equal(1, sender.Stats().Dropped)
}

func (s *SenderSuite) newSender(path string) *Sender {
	sender, err := NewSender(SenderConfig{Path: path, Retry: fastRetry})
	s.Require().NoError(err)
	return sender
}

func (s *SenderSuite) runSender(path string) *Sender {
	sender := s.newSender(path)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sender.Run(ctx) }()
	s.T().Cleanup(func() {
		cancel()
		<-done
	})
	return sender
}

func (s *SenderSuite) waitHealthy(sender *Sender, want bool) {
	s.Eventually(func() bool { return sender.Healthy() == want }, waitFor, waitTick)
}
