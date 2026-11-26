package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
)

func fast(r Runner) *Provider {
	p := New(r)
	p.settle = time.Millisecond
	return p
}

// cancelAt returns a context that ends when the daemon receives call n.
func (s *ProviderSuite) cancelAt(d *daemon, n int) context.Context {
	ctx, cancel := context.WithCancel(s.T().Context())
	s.T().Cleanup(cancel)
	d.before = func([]string) {
		if len(d.commands()) == n {
			cancel()
		}
	}
	return ctx
}

// slowAt makes call n outlast the provider's create timeout, as a hung
// daemon would. The provider kills it, and the daemon may still finish it.
func slowAt(d *daemon, n int) *Provider {
	d.before = func([]string) {
		if len(d.commands()) == n {
			time.Sleep(60 * time.Millisecond)
		}
	}
	p := fast(d)
	p.createBound = 20 * time.Millisecond
	return p
}

func (s *ProviderSuite) TestSpawnRemovesAnObjectThatLandsAfterTheCreateWasKilled() {
	spec := s.spec()
	for _, create := range []int{3, 4} {
		for late := 1; late <= 4; late++ {
			s.Run(fmt.Sprintf("create %d lands before call %d", create, late), func() {
				d := &daemon{lateAfter: late}
				err := slowAt(d, create).Spawn(s.T().Context(), spec)
				s.Require().ErrorIs(err, context.DeadlineExceeded)
				d.flushLate()
				s.noneFor(d, spec)
			})
		}
	}
}

func (s *ProviderSuite) TestRollbackSweepsTwiceCleanAfterTheLastRemoval() {
	spec := s.spec()
	d := &daemon{lateAfter: 3}
	s.Require().ErrorIs(slowAt(d, 3).Spawn(s.T().Context(), spec), context.DeadlineExceeded)
	cmds := d.commands()
	lastRm := -1
	for i, c := range cmds {
		if c[1] == "rm" {
			lastRm = i
		}
	}
	s.Require().GreaterOrEqual(lastRm, 0, "the late object was never found")
	lists := 0
	for _, c := range cmds[lastRm+1:] {
		if c[1] == "ls" {
			lists++
		}
	}
	s.GreaterOrEqual(lists, 4, "two clean sweeps of two kinds each")
}

func (s *ProviderSuite) TestSpawnSaysSoWhenLeftoversCannotBeConfirmedGone() {
	spec := s.spec()
	d := &daemon{failAt: 4}
	var armed bool
	d.before = func(args []string) {
		if args[0] == kindNetwork && args[1] == verbCreate {
			armed = true
		}
		if armed && args[0] == kindNetwork && args[1] == "ls" {
			d.add(
				kindNetwork,
				fmt.Sprintf("late-%d", len(d.commands())),
				labelsFor("a", spec.Session),
			)
		}
	}
	p := fast(d)
	p.bound = 100 * time.Millisecond
	err := p.Spawn(s.T().Context(), spec)
	s.ErrorIs(err, errInjected)
	s.ErrorIs(err, ErrReclaimIncomplete)
	s.ErrorIs(err, context.DeadlineExceeded)
}

type ctxSpy struct {
	Runner
	mu   sync.Mutex
	seen []ctxSeen
}

type ctxSeen struct {
	deadline time.Time
	has      bool
	before   error
	after    error
}

func (c *ctxSpy) Run(ctx context.Context, args ...string) ([]byte, error) {
	dl, ok := ctx.Deadline()
	before := ctx.Err()
	out, err := c.Runner.Run(ctx, args...)
	c.mu.Lock()
	c.seen = append(c.seen, ctxSeen{dl, ok, before, ctx.Err()})
	c.mu.Unlock()
	return out, err
}

func (s *ProviderSuite) spawnCanceledAtTheNetworkCreate() (*daemon, *ctxSpy, time.Time) {
	d := &daemon{}
	spy := &ctxSpy{Runner: d}
	ctx := s.cancelAt(d, 3)
	start := time.Now()
	s.Require().ErrorIs(fast(spy).Spawn(ctx, s.spec()), context.Canceled)
	return d, spy, start
}

func (s *ProviderSuite) TestACreateInFlightFinishesWhenTheContextEndsAndNothingFollowsIt() {
	d, spy, start := s.spawnCanceledAtTheNetworkCreate()
	create := spy.seen[2]
	s.NoError(create.after, "the network create was killed")
	s.True(create.has)
	s.False(create.deadline.After(time.Now().Add(30 * time.Second)))
	s.True(create.deadline.After(start.Add(25 * time.Second)))
	for _, c := range d.commands() {
		s.False(c[0] == nounContainer && c[1] == verbCreate, "container create ran: %v", c)
	}
}

func (s *ProviderSuite) TestRollbackIgnoresTheCancelButEndsWithinThirtySeconds() {
	_, spy, start := s.spawnCanceledAtTheNetworkCreate()
	rollback := spy.seen[3:]
	s.Require().NotEmpty(rollback)
	for _, c := range rollback {
		s.True(c.has, "a rollback command ran without a deadline")
		s.NoError(c.before)
		s.False(c.deadline.After(time.Now().Add(30 * time.Second)))
		s.True(c.deadline.After(start.Add(25 * time.Second)))
	}
}

func (s *ProviderSuite) TestSpawnTrustsNoIDButAValidOne() {
	for _, out := range []string{" ", "not-an-id", "--privileged", "abc123", "x\ny"} {
		s.Run(strings.TrimSpace(out), func() {
			spec := s.spec()
			d := &daemon{createOut: out}
			s.Require().Error(fast(d).Spawn(s.T().Context(), spec))
			for _, c := range d.commands() {
				s.False(c[0] == nounContainer && c[1] == verbCreate, "container create ran: %v", c)
			}
			s.noneFor(d, spec)
		})
	}
}

func (s *ProviderSuite) TestASpawnDoesNotHoldUpOtherSessionsAndEveryWaitEndsWithItsContext() {
	spec := s.spec()
	entered, release := make(chan struct{}), make(chan struct{})
	var once, closed sync.Once
	s.T().Cleanup(func() { closed.Do(func() { close(release) }) })
	s.d.before = func(args []string) {
		if args[0] == kindNetwork && args[1] == verbCreate &&
			args[len(args)-1] == resourcePrefix+spec.Session {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { done <- s.p.Spawn(s.T().Context(), spec) }()
	<-entered

	other := s.sessionID("a")
	s.NoError(within(func() error { return s.p.Reclaim(s.T().Context(), "a", other) }))
	s.NoError(within(func() error {
		return s.p.Spawn(
			s.T().Context(),
			provider.Spec{Pool: "a", Session: other, Image: testImage},
		)
	}))
	for name, call := range map[string]func(context.Context) error{
		"reclaim": func(ctx context.Context) error { return s.p.Reclaim(ctx, "a", spec.Session) },
		"spawn":   func(ctx context.Context) error { return s.p.Spawn(ctx, spec) },
	} {
		ctx, cancel := context.WithTimeout(s.T().Context(), 50*time.Millisecond)
		err := within(func() error { return call(ctx) })
		cancel()
		s.ErrorIs(err, context.DeadlineExceeded, name)
	}

	closed.Do(func() { close(release) })
	s.NoError(<-done)
	s.p.locks.mu.Lock()
	defer s.p.locks.mu.Unlock()
	s.Empty(s.p.locks.m, "lock entries are dropped")
}

var errStuck = errors.New("call did not return")

func within(f func() error) error {
	res := make(chan error, 1)
	go func() { res <- f() }()
	select {
	case err := <-res:
		return err
	case <-time.After(2 * time.Second):
		return errStuck
	}
}
