package providertest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/stretchr/testify/suite"
)

const (
	unlabelledName = "unlabelled"
	otherPoolName  = "other-pool"
)

// Harness supplies a fresh, empty provider per test and direct backend seeding
// for arbitrary labels. OnNextCall runs fn once inside the next operation,
// after it starts and before it changes resources. Inventory returns every
// object in the backend, labelled or not, so the suite can check that objects
// outside a pool are never touched. New and Seed are required.
type Harness struct {
	New        func(t *testing.T) provider.Provider
	Seed       func(t *testing.T, p provider.Provider, r provider.Resource)
	OnNextCall func(t *testing.T, p provider.Provider, fn func())
	Inventory  func(t *testing.T, p provider.Provider) []provider.Resource
}

type contractSuite struct {
	suite.Suite
	harness Harness
	p       provider.Provider
}

// RunContract checks the pool provider interface using harness.
func RunContract(t *testing.T, harness Harness) {
	t.Helper()
	suite.Run(t, &contractSuite{harness: harness})
}

func (s *contractSuite) SetupTest() {
	s.p = s.harness.New(s.T())
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
			s.NotEmpty(r.UID)
		}
	}
}

func (s *contractSuite) TestListReturnsEveryResourceInPool() {
	const seedCount = 1000
	specs := []provider.Spec{s.spec("a"), s.spec("a-b")}
	want := make(map[string][]provider.Resource)
	for _, spec := range specs {
		s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
		want[spec.Pool] = s.withoutUIDs(s.list(spec.Pool))
	}
	for i := range seedCount {
		pool := specs[i%len(specs)].Pool
		r := provider.Resource{
			Kind: networkKind,
			Name: fmt.Sprintf("seed-%04d", i),
			Labels: map[string]string{
				provider.LabelPool:    pool,
				provider.LabelSession: "invalid",
			},
		}
		s.seed(r)
		want[pool] = append(want[pool], r)
	}
	s.seed(provider.Resource{Kind: networkKind, Name: "unlabelled"})
	for _, spec := range specs {
		s.ElementsMatch(want[spec.Pool], s.withoutUIDs(s.list(spec.Pool)))
	}
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
	s.seed(provider.Resource{
		Kind: networkKind,
		Name: "foreign-session",
		Labels: map[string]string{
			provider.LabelPool:    other.Pool,
			provider.LabelSession: first.Session,
		},
	})
	otherResources := s.list(other.Pool)
	s.Require().True(slices.ContainsFunc(otherResources, func(r provider.Resource) bool {
		return r.Name == "foreign-session" && r.Labels[provider.LabelSession] == first.Session
	}))
	s.Require().NoError(s.p.Spawn(ctx, first))
	s.Require().NoError(s.p.Reclaim(ctx, first.Pool, first.Session))
	s.ElementsMatch(keep, s.list(first.Pool))
	s.ElementsMatch(otherResources, s.list(other.Pool))
	s.Require().NoError(s.p.Reclaim(ctx, first.Pool, first.Session))
	s.ElementsMatch(keep, s.list(first.Pool))
}

func (s *contractSuite) TestSpawnRejectsInvalidSessionIDs() {
	ctx := s.T().Context()
	for _, id := range s.invalidSessionIDs() {
		s.Run(id, func() {
			spec := provider.Spec{Pool: "a", Session: id}
			s.ErrorIs(s.p.Spawn(ctx, spec), provider.ErrInvalidSession)
			for _, pool := range []string{spec.Pool, "a-b", "b"} {
				s.Empty(s.list(pool))
			}
		})
	}
}

func (s *contractSuite) TestReclaimRejectsInvalidSessionIDs() {
	for _, id := range s.invalidSessionIDs() {
		s.Run(id, func() {
			const pool = "a"
			s.seed(provider.Resource{
				Kind: networkKind,
				Name: id,
				Labels: map[string]string{
					provider.LabelPool:    pool,
					provider.LabelSession: id,
				},
			})
			before := s.list(pool)
			s.Require().NotEmpty(before)
			s.ErrorIs(s.p.Reclaim(s.T().Context(), pool, id), provider.ErrInvalidSession)
			s.ElementsMatch(before, s.list(pool))
		})
	}
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
	s.seed(provider.Resource{
		Kind: networkKind,
		Name: "foreign-orphan",
		Labels: map[string]string{
			provider.LabelPool: victim.Pool,
		},
	})
	before := s.list(victim.Pool)
	s.Require().
		True(slices.ContainsFunc(before, func(r provider.Resource) bool { return r.Name == "foreign-orphan" }))
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

func (s *contractSuite) TestDeleteIgnoresStaleUID() {
	ctx := s.T().Context()
	spec := s.spec("a")
	s.Require().NoError(s.p.Spawn(ctx, spec))
	first := s.list(spec.Pool)
	s.Require().NotEmpty(first)
	s.Require().NoError(s.p.Reclaim(ctx, spec.Pool, spec.Session))
	s.Require().NoError(s.p.Spawn(ctx, spec))
	replacement := s.list(spec.Pool)
	liveUIDs := make(map[resourceKey]string, len(replacement))
	for _, r := range replacement {
		liveUIDs[keyFor(r)] = r.UID
	}
	for _, r := range first {
		s.Require().NotEmpty(r.UID)
		s.Require().NotEmpty(liveUIDs[keyFor(r)], "%s/%s was not recreated", r.Kind, r.Name)
		s.Require().NotEqual(r.UID, liveUIDs[keyFor(r)], "%s/%s", r.Kind, r.Name)
		s.Require().NoError(s.p.Delete(ctx, spec.Pool, r))
	}
	s.ElementsMatch(replacement, s.list(spec.Pool))
}

func (s *contractSuite) TestDeleteRequiresUID() {
	spec := s.spec("a")
	ctx := s.T().Context()
	s.Require().NoError(s.p.Spawn(ctx, spec))
	before := s.list(spec.Pool)
	s.Require().NotEmpty(before)
	r := before[0]
	r.UID = ""
	s.ErrorIs(s.p.Delete(ctx, spec.Pool, r), provider.ErrUIDRequired)
	s.ElementsMatch(before, s.list(spec.Pool))
}

func (s *contractSuite) TestUnlabelledAndForeignObjectsAreNeverTouched() {
	if s.harness.Inventory == nil {
		s.T().Skip("a real provider must supply Harness.Inventory")
	}
	ctx := s.T().Context()
	spec := s.spec("a")
	s.Require().NoError(s.p.Spawn(ctx, spec))
	s.seed(provider.Resource{Kind: networkKind, Name: unlabelledName})
	s.seed(provider.Resource{
		Kind: networkKind,
		Name: otherPoolName,
		Labels: map[string]string{
			provider.LabelPool:    "a-b",
			provider.LabelSession: spec.Session,
		},
	})
	untouched := s.untouchedInventory()
	s.Require().Len(untouched, 2)
	for _, r := range s.list(spec.Pool) {
		s.Require().NoError(s.p.Delete(ctx, spec.Pool, r))
	}
	s.Require().NoError(s.p.Reclaim(ctx, spec.Pool, spec.Session))
	s.Require().NoError(s.p.Spawn(ctx, spec))
	s.Require().NoError(s.p.Reclaim(ctx, spec.Pool, spec.Session))
	s.ElementsMatch(untouched, s.untouchedInventory())
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

func (s *contractSuite) TestCancellationInsideOperationsLeavesResourcesUnchanged() {
	if s.harness.OnNextCall == nil {
		s.T().Skip("a real provider must supply Harness.OnNextCall")
	}
	spec, pending := s.spec("a"), s.spec("a")
	s.Require().NoError(s.p.Spawn(s.T().Context(), spec))
	before := s.list(spec.Pool)
	s.Require().NotEmpty(before)
	for _, op := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"Spawn", func(ctx context.Context) error { return s.p.Spawn(ctx, pending) }},
		{"Reclaim", func(ctx context.Context) error { return s.p.Reclaim(ctx, spec.Pool, spec.Session) }},
		{"Delete", func(ctx context.Context) error { return s.p.Delete(ctx, spec.Pool, before[0]) }},
	} {
		s.Run(op.name, func() {
			ctx, cancel := context.WithCancel(s.T().Context())
			defer cancel()
			s.harness.OnNextCall(s.T(), s.p, cancel)
			s.ErrorIs(op.run(ctx), context.Canceled)
			s.ElementsMatch(before, s.list(spec.Pool))
		})
	}
}

func (s *contractSuite) TestRejectsInvalidPoolNames() {
	ctx := s.T().Context()
	spec := s.spec("a")
	s.Require().NoError(s.p.Spawn(ctx, spec))
	before := s.list(spec.Pool)
	s.Require().NotEmpty(before)
	for i, pool := range []string{"", "A", "a_b", strings.Repeat("a", 33)} {
		s.Run("pool="+pool, func() {
			s.seed(provider.Resource{
				Kind:   networkKind,
				Name:   fmt.Sprintf("invalid-pool-%d", i),
				Labels: map[string]string{provider.LabelPool: pool},
			})
			s.ErrorIs(
				s.p.Spawn(ctx, provider.Spec{Pool: pool, Session: spec.Session}),
				provider.ErrInvalidPool,
				"Spawn",
			)
			s.ErrorIs(s.p.Reclaim(ctx, pool, spec.Session), provider.ErrInvalidPool, "Reclaim")
			resources, err := s.p.List(ctx, pool)
			s.ErrorIs(err, provider.ErrInvalidPool, "List")
			s.Empty(resources)
			s.ErrorIs(s.p.Delete(ctx, pool, before[0]), provider.ErrInvalidPool, "Delete")
			s.ElementsMatch(before, s.list(spec.Pool))
		})
	}
}

func (s *contractSuite) untouchedInventory() []provider.Resource {
	s.T().Helper()
	var out []provider.Resource
	for _, r := range s.harness.Inventory(s.T(), s.p) {
		if r.Name == unlabelledName || r.Name == otherPoolName {
			out = append(out, r)
		}
	}
	return out
}

func (s *contractSuite) seed(r provider.Resource) {
	s.T().Helper()
	s.harness.Seed(s.T(), s.p, r)
}

func (s *contractSuite) withoutUIDs(resources []provider.Resource) []provider.Resource {
	s.T().Helper()
	result := make([]provider.Resource, len(resources))
	for i, r := range resources {
		s.NotEmpty(r.UID, "%s/%s", r.Kind, r.Name)
		r.UID = ""
		result[i] = r
	}
	return result
}

func (s *contractSuite) invalidSessionIDs() []string {
	return []string{s.spec("a-b").Session, s.spec("b").Session, "invalid"}
}

func (s *contractSuite) list(pool string) []provider.Resource {
	s.T().Helper()
	resources, err := s.p.List(s.T().Context(), pool)
	s.Require().NoError(err)
	return resources
}

func (s *contractSuite) spec(pool string) provider.Spec {
	s.T().Helper()
	id, err := session.NewID(pool)
	s.Require().NoError(err)
	return provider.Spec{Pool: pool, Session: id}
}
