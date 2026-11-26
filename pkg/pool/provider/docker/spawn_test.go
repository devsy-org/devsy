package docker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
)

const testImage = "busybox:1.37"

func (s *ProviderSuite) TestSpawnCreatesAnInternalNetworkAndALabelledContainer() {
	spec := s.spec()
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	network, ok := s.d.named(kindNetwork, resourcePrefix+spec.Session)
	s.Require().True(ok)
	s.True(network.internal)
	s.Equal(labelsFor("a", spec.Session), network.labels)
	ws, ok := s.d.named(kindWorkspace, resourcePrefix+spec.Session+"-workspace")
	s.Require().True(ok)
	s.Equal(network.id, ws.network)
	s.Equal(testImage, ws.image)
	s.Equal(labelsFor("a", spec.Session), ws.labels)
	s.Len(s.list(), 2)
}

func (s *ProviderSuite) TestSpawnPassesTheImageAfterTheTerminator() {
	for _, image := range []string{"busybox", "reg:5000/x/y:v1@sha256:ab", "a.b/c_d/e-f"} {
		spec := s.spec()
		spec.Image = image
		s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	}
	creates := 0
	for _, c := range s.d.commands() {
		if c[1] != verbCreate || c[0] != nounContainer {
			continue
		}
		creates++
		n := len(c)
		s.Equal("--", c[n-2], "%v", c)
		s.NotContains(c[:n-2], c[n-1])
	}
	s.Equal(3, creates)
}

func (s *ProviderSuite) TestSpawnRejectsBadInputsBeforeAnyCommand() {
	ctx := s.T().Context()
	spec := s.spec()
	for _, image := range []string{"", "-x", "--privileged", "a b", "a\tb", "a\nb", "a\x00b", "$(id) x"} {
		bad := spec
		bad.Image = image
		s.ErrorIs(s.p.Spawn(ctx, bad), provider.ErrInvalidImage, "%q", image)
	}
	s.ErrorIs(s.p.Spawn(ctx, provider.Spec{Pool: "A", Session: spec.Session, Image: testImage}),
		provider.ErrInvalidPool)
	s.ErrorIs(s.p.Spawn(ctx, provider.Spec{Pool: "a", Session: "x", Image: testImage}),
		provider.ErrInvalidSession)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	s.ErrorIs(s.p.Spawn(canceled, spec), context.Canceled)
	s.Empty(s.d.commands())
}

func (s *ProviderSuite) TestSpawnReportsExistingResourcesAndChangesNothing() {
	spec := s.spec()
	s.d.add(kindWorkspace, "leftover", labelsFor("a", spec.Session))
	s.Require().ErrorIs(s.p.Spawn(s.T().Context(), spec), provider.ErrExists)
	for _, c := range s.d.commands() {
		s.NotEqual(verbCreate, c[1], "%v", c)
	}
	s.Equal(1, s.d.count())
}

func (s *ProviderSuite) TestSpawnLeavesNothingBehindWhenAnyCommandFails() {
	spec := s.spec()
	clean := &daemon{}
	s.Require().NoError(New(clean).Spawn(s.T().Context(), spec))
	total := len(clean.commands())
	for n := 1; n <= total; n++ {
		for _, applied := range []bool{false, true} {
			s.Run(fmt.Sprintf("call %d applied %v", n, applied), func() {
				d := &daemon{failAt: n, failApplied: applied}
				unrelated := d.add(kindNetwork, "unrelated", nil)
				err := New(d).Spawn(s.T().Context(), spec)
				s.Require().ErrorIs(err, errInjected)
				s.NotErrorIs(err, provider.ErrExists)
				s.noneFor(d, spec)
				s.True(d.has(unrelated))
			})
		}
	}
}

func (s *ProviderSuite) TestSpawnCleansUpWhenCanceledAtAnyCommand() {
	spec := s.spec()
	clean := &daemon{}
	s.Require().NoError(New(clean).Spawn(s.T().Context(), spec))
	for n := 1; n <= len(clean.commands()); n++ {
		s.Run(fmt.Sprintf("call %d", n), func() {
			d := &daemon{}
			ctx, cancel := context.WithCancel(s.T().Context())
			defer cancel()
			d.before = func([]string) {
				if len(d.commands()) == n {
					cancel()
				}
			}
			s.Require().ErrorIs(New(d).Spawn(ctx, spec), context.Canceled)
			s.noneFor(d, spec)
		})
	}
}

func (s *ProviderSuite) TestSpawnReportsAFailedRollbackAndAReclaimFinishesIt() {
	spec := s.spec()
	boom := errors.New("daemon refused")
	d := &daemon{failAt: 4, rmErr: boom}
	p := New(d)
	err := p.Spawn(s.T().Context(), spec)
	s.Require().ErrorIs(err, errInjected)
	s.Require().ErrorIs(err, boom)
	s.Contains(err.Error(), "rollback of session "+spec.Session)
	s.Equal(1, d.count(), "the network stays for a later reclaim")
	d.rmErr = nil
	s.Require().NoError(p.Reclaim(s.T().Context(), "a", spec.Session))
	s.noneFor(d, spec)
}

func (s *ProviderSuite) TestSpawnNeverRemovesAnObjectItDidNotCreate() {
	spec := s.spec()
	taken := s.d.add(kindNetwork, resourcePrefix+spec.Session, nil)
	s.Require().Error(s.p.Spawn(s.T().Context(), spec))
	s.True(s.d.has(taken))
	s.Equal(1, s.d.count())
}

func (s *ProviderSuite) TestTwoSpawnsOfOneSessionMakeOneSet() {
	spec := s.spec()
	var creates atomic.Int32
	arrived := make(chan struct{})
	// Both Spawns pass the existence check before either creates anything.
	// Behind the provider's lock the second never gets that far, so the
	// first one's wait times out and the Spawns run one after the other.
	s.d.before = func(args []string) {
		if args[0] != kindNetwork || args[1] != verbCreate {
			return
		}
		switch creates.Add(1) {
		case 1:
			select {
			case <-arrived:
			case <-time.After(300 * time.Millisecond):
			}
		case 2:
			close(arrived)
		}
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = s.p.Spawn(s.T().Context(), spec) })
	}
	wg.Wait()
	s.ElementsMatch([]error{nil, provider.ErrExists}, errs)
	s.Equal(2, s.d.count())
}

func (s *ProviderSuite) spec() provider.Spec {
	s.T().Helper()
	return provider.Spec{Pool: "a", Session: s.sessionID("a"), Image: testImage}
}

func (s *ProviderSuite) noneFor(d *daemon, spec provider.Spec) {
	s.T().Helper()
	for _, r := range d.inventory() {
		s.NotEqual(
			spec.Session,
			r.Labels["devsy.sh/pool-session"],
			"%s %s left behind",
			r.Kind,
			r.Name,
		)
	}
}
