package docker

import (
	"context"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
)

func (s *ProviderSuite) TestStartStartsOnlyTheSessionsWorkspace() {
	spec, other := s.spec(), s.spec()
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	s.Require().NoError(s.p.Spawn(s.T().Context(), other))
	s.d.seed(kindWorkspace, "foreign", labelsFor("a-b", spec.Session))
	s.Require().NoError(s.p.Start(s.T().Context(), "a", spec.Session))
	started := 0
	for _, o := range s.d.snapshot() {
		started += o.starts
		if o.starts > 0 {
			s.Equal(resourcePrefix+spec.Session+"-workspace", o.name)
		}
	}
	s.Equal(1, started)
}

func (s *ProviderSuite) TestStartIsIdempotent() {
	spec := s.spec()
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	before := s.d.inventory()
	for range 2 {
		s.Require().NoError(s.p.Start(s.T().Context(), "a", spec.Session))
	}
	s.ElementsMatch(before, s.d.inventory())
}

func (s *ProviderSuite) TestStartWithoutAWorkspaceIsNotFound() {
	spec := s.spec()
	s.ErrorIs(s.p.Start(s.T().Context(), "a", spec.Session), provider.ErrNotFound)
	s.d.seed(kindNetwork, "network-only", labelsFor("a", spec.Session))
	s.ErrorIs(s.p.Start(s.T().Context(), "a", spec.Session), provider.ErrNotFound)
	for _, c := range s.d.commands() {
		s.NotEqual(verbStart, c[1])
	}
}

func (s *ProviderSuite) TestStartRefusesAForeignSessionBeforeTouchingDocker() {
	s.Require().ErrorIs(
		s.p.Start(s.T().Context(), "a", s.sessionID("a-b")),
		provider.ErrInvalidSession,
	)
	s.Require().ErrorIs(
		s.p.Start(s.T().Context(), "A", s.sessionID("a")),
		provider.ErrInvalidPool,
	)
	s.Empty(s.d.commands())
}

func (s *ProviderSuite) TestStartPassesTheContainerIDAfterTheTerminator() {
	spec := s.spec()
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	s.Require().NoError(s.p.Start(s.T().Context(), "a", spec.Session))
	ws, _ := s.d.named(kindWorkspace, resourcePrefix+spec.Session+"-workspace")
	var starts [][]string
	for _, c := range s.d.commands() {
		if c[1] == verbStart {
			starts = append(starts, c)
		}
	}
	s.Equal([][]string{{nounContainer, verbStart, "--", ws.id}}, starts)
}

func (s *ProviderSuite) TestStartReportsADockerFailureWithoutTheSessionLock() {
	spec := s.spec()
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	s.d.failAt = len(s.d.commands()) + 3
	err := s.p.Start(s.T().Context(), "a", spec.Session)
	s.Require().ErrorIs(err, errInjected)
	s.Require().NoError(s.p.Start(s.T().Context(), "a", spec.Session))
	s.Require().NoError(s.p.Reclaim(s.T().Context(), "a", spec.Session))
}

func (s *ProviderSuite) TestStartWaitsForAReclaimOfTheSameSession() {
	spec := s.spec()
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	held, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	s.T().Cleanup(open)
	s.d.once = func() {
		close(held)
		<-release
	}
	reclaimed := make(chan error, 1)
	go func() { reclaimed <- s.p.Reclaim(s.T().Context(), "a", spec.Session) }()
	<-held
	busy := len(s.d.commands())
	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()
	waiting := make(chan error, 1)
	go func() { waiting <- s.p.Start(ctx, "a", spec.Session) }()
	select {
	case err := <-waiting:
		s.Require().Failf("Start did not wait for the lock", "returned %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.Len(s.d.commands(), busy)
	cancel()
	s.ErrorIs(<-waiting, context.Canceled)
	open()
	s.Require().NoError(<-reclaimed)
	s.ErrorIs(s.p.Start(s.T().Context(), "a", spec.Session), provider.ErrNotFound)
}
