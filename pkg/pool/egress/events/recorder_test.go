package events

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy-signals/attrkey"
	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/suite"
)

const (
	testSession = "p-test-pool-abcdefghij"
	hostA       = "a.example.com"
	hostB       = "b.example.com"
	hostC       = "c.example.com"
	agentName   = "claude"
)

type fakeSink struct {
	mu     sync.Mutex
	events []audit.Event
	calls  int
	fail   error
	// during runs inside the first Write, before it fails, with no lock held.
	during func()
	// failIf fails the writes it returns an error for.
	failIf func(audit.Event) error
}

func (f *fakeSink) Write(e audit.Event) error {
	f.mu.Lock()
	hook := f.during
	f.during = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail != nil {
		return f.fail
	}
	if f.failIf != nil {
		if err := f.failIf(e); err != nil {
			return err
		}
	}
	f.events = append(f.events, e)
	return nil
}

func (f *fakeSink) setFail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

func (f *fakeSink) written() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Event(nil), f.events...)
}

func (f *fakeSink) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type RecorderSuite struct {
	suite.Suite
	sink  *fakeSink
	clock *clock
}

func TestRecorderSuite(t *testing.T) {
	suite.Run(t, new(RecorderSuite))
}

func (s *RecorderSuite) SetupTest() {
	s.sink = &fakeSink{}
	s.clock = &clock{now: time.Date(2026, time.November, 28, 10, 0, 0, 0, time.UTC)}
}

func newRecorder(s *RecorderSuite, mutate func(*Config)) *Recorder {
	cfg := Config{
		Sink:    s.sink,
		Session: testSession,
		Actor:   envelope.Actor{Kind: envelope.ActorAgent, ID: agentName},
		Now:     s.clock.Now,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	r, err := New(cfg)
	s.Require().NoError(err)
	return r
}

func showAll(string) bool { return true }

func denial(host string, port int, reason egress.Reason) egress.Denial {
	return egress.Denial{Target: egress.Target{Host: host, Port: port}, Reason: reason}
}

func (s *RecorderSuite) TestADenialIsAnEventWithARedactedAddress() {
	r := newRecorder(s, nil)
	r.Denied(denial(hostA, 443, egress.NotAllowed))

	events := s.sink.written()
	s.Require().Len(events, 1)
	e := events[0]
	s.Equal(envelope.AuditEgressDeny, e.Type)
	s.Equal(testSession, e.Session)
	s.Equal(envelope.Actor{Kind: envelope.ActorAgent, ID: agentName}, e.Actor)
	s.Equal(map[string]any{
		attrkey.ServerPort:            443,
		attrkey.EgressReason:          "not_allowed",
		attrkey.EgressAddressRedacted: true,
	}, e.Data)
}

func (s *RecorderSuite) TestAnAddressAppearsOnlyWhenTheFilterAllowsIt() {
	r := newRecorder(s, func(c *Config) {
		c.ShowAddress = func(host string) bool { return host != hostB }
	})
	r.Denied(denial(hostA, 443, egress.NotAllowed))
	r.Denied(denial(hostB, 443, egress.NotAllowed))
	r.Denied(egress.Denial{
		Target: egress.Target{Host: hostC, Port: 443, AddressRedacted: true},
		Reason: egress.BadRequest,
	})

	events := s.sink.written()
	s.Require().Len(events, 3)
	s.Equal(hostA, events[0].Data[attrkey.ServerAddress])
	s.NotContains(events[0].Data, attrkey.EgressAddressRedacted)
	for _, e := range events[1:] {
		s.NotContains(e.Data, attrkey.ServerAddress)
		s.Equal(true, e.Data[attrkey.EgressAddressRedacted])
	}
}

func (s *RecorderSuite) TestRepeatedDenialsCoalesceUntilTheNextFlush() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	for range 5 {
		r.Denied(denial(hostA, 443, egress.NotAllowed))
	}
	s.Len(s.sink.written(), 1)

	r.Denied(denial(hostA, 443, egress.PrivateRange))
	r.Denied(denial(hostA, 8443, egress.NotAllowed))
	r.Denied(denial(hostB, 443, egress.NotAllowed))
	s.Len(s.sink.written(), 4, "a new reason, port or host is a new key")

	s.Require().NoError(r.Flush())
	r.Denied(denial(hostA, 443, egress.NotAllowed))
	s.Len(s.sink.written(), 5, "a new window writes the denial again")
}

func (s *RecorderSuite) TestDenialsPastTheKeyCapAreDroppedAndCounted() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll; c.MaxKeys = 2 })
	r.Denied(denial(hostA, 443, egress.NotAllowed))
	r.Denied(denial(hostB, 443, egress.NotAllowed))
	r.Denied(denial(hostC, 443, egress.NotAllowed))
	r.Denied(denial(hostC, 443, egress.NotAllowed))
	s.Len(s.sink.written(), 2)
	s.Equal(2, r.Stats().Dropped)

	s.Require().NoError(r.Flush())
	r.Denied(denial(hostC, 443, egress.NotAllowed))
	s.Len(s.sink.written(), 3, "the cap is per window")
}

func (s *RecorderSuite) TestWhatTheAllowListCannotHoldIsCountedNotWritten() {
	r := newRecorder(s, nil)
	r.Denied(denial("", 0, egress.BadRequest))
	r.Denied(denial(hostA, 65536, egress.NotAllowed))
	r.Denied(denial(hostA, 443, egress.Reason("made_up")))
	r.Denied(denial(hostA, 443, ""))
	r.Allowed(egress.Target{Host: hostA, Port: 0})
	s.Empty(s.sink.written())
	s.Equal(5, r.Stats().Unrecorded)
	s.Require().NoError(r.Flush())
	s.Empty(s.sink.written())
}

func (s *RecorderSuite) TestEveryProxyReasonIsAccepted() {
	r := newRecorder(s, nil)
	reasons := []egress.Reason{
		egress.NotAllowed, egress.ReservedRange, egress.PrivateRange, egress.PoolNetwork,
		egress.IPLiteral, egress.ResolveFailed, egress.BadRequest, egress.SNIMismatch,
		egress.SNIMissing,
	}
	for _, reason := range reasons {
		r.Denied(denial(hostA, 443, reason))
	}
	s.Len(s.sink.written(), len(reasons))
	s.Zero(r.Stats().Unrecorded)
}

func (s *RecorderSuite) TestAllowedConnectionsAreSummarizedPerDestination() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	first := s.clock.Now()
	r.Allowed(egress.Target{Host: hostB, Port: 443})
	s.clock.advance(time.Second)
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	s.clock.advance(time.Second)
	r.Allowed(egress.Target{Host: hostB, Port: 443})
	s.clock.advance(time.Second)
	r.Allowed(egress.Target{Host: hostA, Port: 8443})
	last := s.clock.Now()
	s.Empty(s.sink.written(), "nothing is written per connection")

	s.Require().NoError(r.Flush())
	events := s.sink.written()
	s.Require().Len(events, 3)
	for _, e := range events {
		s.Equal(envelope.AuditEgressAllowSummary, e.Type)
		s.Equal(testSession, e.Session)
	}
	want := []map[string]any{
		{
			attrkey.ServerAddress: hostA, attrkey.ServerPort: 443, attrkey.EgressCount: 1,
			attrkey.EgressFirstSeen: envelope.FormatTime(first.Add(time.Second)),
			attrkey.EgressLastSeen:  envelope.FormatTime(first.Add(time.Second)),
		},
		{
			attrkey.ServerAddress: hostA, attrkey.ServerPort: 8443, attrkey.EgressCount: 1,
			attrkey.EgressFirstSeen: envelope.FormatTime(last),
			attrkey.EgressLastSeen:  envelope.FormatTime(last),
		},
		{
			attrkey.ServerAddress: hostB, attrkey.ServerPort: 443, attrkey.EgressCount: 2,
			attrkey.EgressFirstSeen: envelope.FormatTime(first),
			attrkey.EgressLastSeen:  envelope.FormatTime(first.Add(2 * time.Second)),
		},
	}
	for i, e := range events {
		s.Equal(want[i], e.Data, i)
	}

	s.Require().NoError(r.Flush())
	s.Len(s.sink.written(), 3, "a flush with nothing counted writes nothing")
}

func (s *RecorderSuite) TestRedactedAllowsShareOneSummaryPerPort() {
	r := newRecorder(s, nil)
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	r.Allowed(egress.Target{Host: hostB, Port: 443})
	r.Allowed(egress.Target{Port: 443, AddressRedacted: true})
	s.Require().NoError(r.Flush())
	events := s.sink.written()
	s.Require().Len(events, 1)
	s.Equal(3, events[0].Data[attrkey.EgressCount])
	s.Equal(true, events[0].Data[attrkey.EgressAddressRedacted])
	s.NotContains(events[0].Data, attrkey.ServerAddress)
}

func (s *RecorderSuite) TestNamedDestinationsPastTheCapFoldIntoARedactedBucket() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll; c.MaxKeys = 2 })
	for _, host := range []string{hostA, hostB, hostC, hostC, hostA} {
		r.Allowed(egress.Target{Host: host, Port: 443})
	}
	s.Require().NoError(r.Flush())
	counts := map[string]int{}
	for _, e := range s.sink.written() {
		host, _ := e.Data[attrkey.ServerAddress].(string)
		counts[host] += e.Data[attrkey.EgressCount].(int)
	}
	s.Equal(map[string]int{hostA: 2, hostB: 1, "": 2}, counts)
}

func (s *RecorderSuite) TestAFailedDenialWriteIsRetriedAtTheNextFlush() {
	r := newRecorder(s, nil)
	s.sink.setFail(errors.New("disk full"))
	for range 5 {
		r.Denied(denial(hostA, 443, egress.NotAllowed))
	}
	s.Equal(1, s.sink.attempts(), "an agent cannot drive retries by repeating the denial")
	s.Equal(1, r.Stats().Failures)

	s.sink.setFail(nil)
	s.Require().NoError(r.Flush())
	events := s.sink.written()
	s.Require().Len(events, 1)
	s.Equal(envelope.AuditEgressDeny, events[0].Type)
	s.Zero(r.Stats().Failures, "a success ends the run of failures")

	r.Denied(denial(hostA, 443, egress.NotAllowed))
	s.Len(s.sink.written(), 2, "the retried denial opened a new window")
	s.Require().NoError(r.Flush())
	s.Len(s.sink.written(), 2, "a written denial is not retried again")
}

func (s *RecorderSuite) TestADenialTheWriterKeepsRefusingIsGivenUp() {
	r := newRecorder(s, nil)
	s.sink.setFail(errors.New("refused"))
	r.Denied(denial(hostA, 443, egress.NotAllowed))
	s.Require().Error(r.Flush())
	s.Zero(r.Stats().Lost)
	s.Require().Error(r.Flush())
	s.Equal(1, r.Stats().Lost, "the write at denial time was the first of three")

	s.sink.setFail(nil)
	s.Require().NoError(r.Flush())
	s.Empty(s.sink.written())
}

func (s *RecorderSuite) TestASummaryTheWriterKeepsRefusingDoesNotBlockTheRest() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	s.sink.failIf = func(e audit.Event) error {
		if e.Data[attrkey.ServerAddress] == hostA {
			return errors.New("refused")
		}
		return nil
	}
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	r.Allowed(egress.Target{Host: hostB, Port: 443})
	for range 3 {
		s.Require().Error(r.Flush())
	}
	s.Equal(1, r.Stats().Lost)
	s.Require().NoError(r.Flush())
	events := s.sink.written()
	s.Require().Len(events, 1)
	s.Equal(hostB, events[0].Data[attrkey.ServerAddress])
}

func (s *RecorderSuite) TestAFailedDenialRetryKeepsTheSummaries() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	s.sink.setFail(errors.New("disk full"))
	r.Denied(denial(hostA, 443, egress.NotAllowed))
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	s.Require().Error(r.Flush())

	s.sink.setFail(nil)
	s.Require().NoError(r.Flush())
	events := s.sink.written()
	s.Require().Len(events, 2)
	s.Equal(envelope.AuditEgressDeny, events[0].Type)
	s.Equal(envelope.AuditEgressAllowSummary, events[1].Type)
	s.Equal(1, events[1].Data[attrkey.EgressCount])
}

func (s *RecorderSuite) TestNewTrafficDoesNotResetTheAttemptsOfARefusedSummary() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	s.sink.failIf = func(audit.Event) error { return errors.New("refused") }
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	for range 3 {
		s.sink.during = func() { r.Allowed(egress.Target{Host: hostA, Port: 443}) }
		s.Require().Error(r.Flush())
	}
	s.Equal(1, r.Stats().Lost, "traffic during a flush must not keep a refused event alive")
}

func (s *RecorderSuite) TestJunkDenialsOfOneReasonCannotHideAnother() {
	r := newRecorder(s, func(c *Config) { c.MaxKeys = 2 })
	for port := 1000; port < 1005; port++ {
		r.Denied(denial(hostA, port, egress.NotAllowed))
	}
	s.Len(s.sink.written(), 2)
	s.Equal(3, r.Stats().Dropped)

	r.Denied(denial(hostA, 443, egress.PoolNetwork))
	r.Denied(denial(hostA, 443, egress.SNIMismatch))
	events := s.sink.written()
	s.Require().Len(events, 4)
	s.Equal("pool_network", events[2].Data[attrkey.EgressReason])
	s.Equal("sni_mismatch", events[3].Data[attrkey.EgressReason])
}

func (s *RecorderSuite) TestAFlushLogsWhatWasNotRecordedOnce() {
	var logs bytes.Buffer
	r := newRecorder(s, func(c *Config) {
		c.MaxKeys = 1
		c.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	})
	r.Denied(denial(hostA, 443, egress.NotAllowed))
	r.Denied(denial(hostA, 444, egress.NotAllowed))
	r.Denied(denial(hostA, 0, egress.NotAllowed))
	s.Require().NoError(r.Flush())
	s.Contains(logs.String(), "egress audit events not recorded")
	s.Contains(logs.String(), "dropped=1")
	s.Contains(logs.String(), "unrecorded=1")

	logs.Reset()
	s.Require().NoError(r.Flush())
	s.Empty(logs.String(), "an unchanged count is not logged again")
}

func (s *RecorderSuite) TestRedactedAllowedBucketsAreBounded() {
	r := newRecorder(s, func(c *Config) { c.MaxKeys = 2 })
	for port := 2000; port < 2006; port++ {
		r.Allowed(egress.Target{Host: hostA, Port: port})
	}
	s.Equal(2, r.Stats().Dropped)
	s.Require().NoError(r.Flush())
	s.Len(s.sink.written(), 4)
}

func (s *RecorderSuite) TestAFailedFlushKeepsTheCountsForTheNextOne() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	first := s.clock.Now()
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	r.Allowed(egress.Target{Host: hostB, Port: 443})
	s.sink.setFail(errors.New("disk full"))
	s.Require().Error(r.Flush())
	s.Equal(1, s.sink.attempts(), "a failed flush stops at the first failure")

	s.clock.advance(time.Minute)
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	s.sink.setFail(nil)
	s.Require().NoError(r.Flush())

	events := s.sink.written()
	s.Require().Len(events, 2)
	s.Equal(2, events[0].Data[attrkey.EgressCount])
	s.Equal(envelope.FormatTime(first), events[0].Data[attrkey.EgressFirstSeen])
	s.Equal(envelope.FormatTime(first.Add(time.Minute)), events[0].Data[attrkey.EgressLastSeen])
	s.Equal(1, events[1].Data[attrkey.EgressCount])
}

func (s *RecorderSuite) TestARestoredCountKeepsTheEarliestAndLatestTimes() {
	r := newRecorder(s, func(c *Config) { c.ShowAddress = showAll })
	first := s.clock.Now()
	r.Allowed(egress.Target{Host: hostA, Port: 443})
	s.clock.advance(time.Minute)
	later := s.clock.Now()
	s.sink.setFail(errors.New("disk full"))
	s.sink.during = func() { r.Allowed(egress.Target{Host: hostA, Port: 443}) }
	s.Require().Error(r.Flush())

	s.sink.setFail(nil)
	s.Require().NoError(r.Flush())
	events := s.sink.written()
	s.Require().Len(events, 1)
	s.Equal(2, events[0].Data[attrkey.EgressCount])
	s.Equal(envelope.FormatTime(first), events[0].Data[attrkey.EgressFirstSeen])
	s.Equal(envelope.FormatTime(later), events[0].Data[attrkey.EgressLastSeen])
}

func (s *RecorderSuite) TestRunFlushesOnTheIntervalAndAtTheEnd() {
	r := newRecorder(s, func(c *Config) { c.Interval = 20 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	r.Allowed(egress.Target{Host: hostA, Port: 443})
	s.Eventually(
		func() bool { return len(s.sink.written()) == 1 },
		5*time.Second,
		5*time.Millisecond,
	)

	r.Allowed(egress.Target{Host: hostA, Port: 443})
	cancel()
	s.Require().NoError(<-done)
	events := s.sink.written()
	s.Require().Len(events, 2, "the end of the session flushes the last window")
	s.Equal(1, events[1].Data[attrkey.EgressCount])
}

func (s *RecorderSuite) TestConcurrentUseLosesNoCount() {
	r := newRecorder(s, func(c *Config) { c.MaxKeys = 4 })
	const workers, each = 8, 200
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range each {
				r.Allowed(egress.Target{Host: hostA, Port: 443})
				r.Denied(denial(hostB, 443+i%3, egress.NotAllowed))
				if i%50 == 0 {
					_ = r.Flush()
				}
			}
		})
	}
	wg.Wait()
	s.Require().NoError(r.Flush())

	total := 0
	for _, e := range s.sink.written() {
		if e.Type == envelope.AuditEgressAllowSummary {
			total += e.Data[attrkey.EgressCount].(int)
		}
	}
	s.Equal(workers*each, total)
}

func (s *RecorderSuite) TestNewChecksItsConfig() {
	good := Config{
		Sink: s.sink, Session: testSession,
		Actor: envelope.Actor{Kind: envelope.ActorAgent, ID: agentName},
	}
	r, err := New(good)
	s.Require().NoError(err)
	s.Equal(DefaultInterval, r.cfg.Interval)
	s.Equal(DefaultMaxKeys, r.cfg.MaxKeys)

	with := func(mutate func(*Config)) Config {
		cfg := good
		mutate(&cfg)
		return cfg
	}
	for name, cfg := range map[string]Config{
		"no sink":          with(func(c *Config) { c.Sink = nil }),
		"no session":       with(func(c *Config) { c.Session = "" }),
		"no actor kind":    with(func(c *Config) { c.Actor.Kind = "" }),
		"unknown kind":     with(func(c *Config) { c.Actor.Kind = "robot" }),
		"no actor id":      with(func(c *Config) { c.Actor.ID = "" }),
		"negative window":  with(func(c *Config) { c.Interval = -time.Second }),
		"negative max key": with(func(c *Config) { c.MaxKeys = -1 }),
	} {
		_, err := New(cfg)
		s.Error(err, name)
	}
}
