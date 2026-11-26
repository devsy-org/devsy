package providertest

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/stretchr/testify/suite"
)

type contractSuite struct {
	suite.Suite
	newProvider func(*testing.T) provider.Provider
	p           provider.Provider
}

// RunContract checks the pool provider interface. newProvider supplies a fresh,
// empty provider for each scenario and registers any cleanup with its test.
func RunContract(t *testing.T, newProvider func(t *testing.T) provider.Provider) {
	t.Helper()
	suite.Run(t, &contractSuite{newProvider: newProvider})
}

func (s *contractSuite) SetupTest() {
	s.p = s.newProvider(s.T())
}

func (s *contractSuite) TestSpawnAndList() {
	specs := []provider.Spec{s.spec("a"), s.spec("a-b"), s.spec("ab")}
	for _, spec := range specs {
		s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	}
	for _, spec := range specs {
		resources := s.list(spec.Pool)
		s.Require().NotEmpty(resources)
		for _, r := range resources {
			s.Equal(spec.Pool, r.Labels[provider.LabelPool])
			s.Equal(spec.Session, r.Labels[provider.LabelSession])
		}
	}
	s.Empty(s.list("A"))
}

func (s *contractSuite) TestDuplicateSpawnChangesNothing() {
	spec := s.spec("a")
	ctx := s.T().Context()
	s.Require().NoError(s.p.Spawn(ctx, spec))
	before := s.list(spec.Pool)
	s.Require().NotEmpty(before)
	s.Require().ErrorIs(s.p.Spawn(ctx, spec), provider.ErrExists)
	s.ElementsMatch(before, s.list(spec.Pool))
}

func (s *contractSuite) TestReclaimIsScopedAndIdempotent() {
	ctx := s.T().Context()
	first, second := s.spec("a"), s.spec("a")
	other := s.spec("a-b")
	s.Require().NoError(s.p.Spawn(ctx, second))
	keep := s.list(second.Pool)
	s.Require().NotEmpty(keep)
	s.Require().NoError(s.p.Spawn(ctx, other))
	otherResources := s.list(other.Pool)
	s.Require().NotEmpty(otherResources)
	s.Require().NoError(s.p.Spawn(ctx, first))
	s.Require().NoError(s.p.Reclaim(ctx, first.Pool, first.Session))
	s.ElementsMatch(keep, s.list(first.Pool))
	s.ElementsMatch(otherResources, s.list(other.Pool))
	s.Require().NoError(s.p.Reclaim(ctx, first.Pool, first.Session))
	s.ElementsMatch(keep, s.list(first.Pool))
}

func (s *contractSuite) TestDeleteRemovesOnlyOneAndIsIdempotent() {
	ctx := s.T().Context()
	spec, other := s.spec("a"), s.spec("a")
	s.Require().NoError(s.p.Spawn(ctx, spec))
	s.Require().NoError(s.p.Spawn(ctx, other))
	before := s.list(spec.Pool)
	s.Require().GreaterOrEqual(len(before), 2)
	s.Require().NoError(s.p.Delete(ctx, spec.Pool, before[0]))
	s.ElementsMatch(before[1:], s.list(spec.Pool))
	s.Require().NoError(s.p.Delete(ctx, spec.Pool, before[0]))
	s.ElementsMatch(before[1:], s.list(spec.Pool))
}

func (s *contractSuite) TestDeleteIgnoresForgedPoolLabels() {
	ctx := s.T().Context()
	victim, caller := s.spec("a-b"), s.spec("a")
	s.Require().NoError(s.p.Spawn(ctx, victim))
	s.Require().NoError(s.p.Spawn(ctx, caller))
	before := s.list(victim.Pool)
	s.Require().NotEmpty(before)
	for _, live := range before {
		forged := live
		forged.Labels = map[string]string{
			provider.LabelPool:    caller.Pool,
			provider.LabelSession: caller.Session,
		}
		s.Require().NoError(s.p.Delete(ctx, caller.Pool, forged))
	}
	s.ElementsMatch(before, s.list(victim.Pool))
}

func (s *contractSuite) TestListReturnsLabelCopies() {
	spec := s.spec("a")
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	before, changed := s.list(spec.Pool), s.list(spec.Pool)
	s.Require().NotEmpty(changed)
	delete(changed[0].Labels, provider.LabelPool)
	delete(changed[0].Labels, provider.LabelSession)
	s.ElementsMatch(before, s.list(spec.Pool))
}

func (s *contractSuite) TestCanceledOperationsDoNotChangeResources() {
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	spec := s.spec("a")
	s.Require().ErrorIs(s.p.Spawn(ctx, spec), context.Canceled)
	s.Empty(s.list(spec.Pool))
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	before := s.list(spec.Pool)
	s.Require().NotEmpty(before)
	s.Require().ErrorIs(s.p.Reclaim(ctx, spec.Pool, spec.Session), context.Canceled)
	_, err := s.p.List(ctx, spec.Pool)
	s.Require().ErrorIs(err, context.Canceled)
	s.Require().ErrorIs(s.p.Delete(ctx, spec.Pool, before[0]), context.Canceled)
	s.ElementsMatch(before, s.list(spec.Pool))
}

func (s *contractSuite) list(pool string) []provider.Resource {
	resources, err := s.p.List(s.T().Context(), pool)
	s.Require().NoError(err)
	return resources
}

func (s *contractSuite) spec(pool string) provider.Spec {
	id, err := session.NewID(pool)
	s.Require().NoError(err)
	return provider.Spec{Pool: pool, Session: id}
}
