package providertest

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/stretchr/testify/suite"
)

const exampleImage = "busybox"

var exampleSpec = provider.Spec{Pool: "a", Session: "p-a-xxxxxxxxxx", Image: exampleImage}

type FakeSuite struct{ suite.Suite }

func TestFakeSuite(t *testing.T) { suite.Run(t, new(FakeSuite)) }

func TestFakeContract(t *testing.T) {
	RunContract(t, Harness{
		New: func(*testing.T) provider.Provider { return new(Fake) },
		Seed: func(_ *testing.T, p provider.Provider, r provider.Resource) {
			p.(*Fake).Add(r)
		},
		Inventory: func(_ *testing.T, p provider.Provider) []provider.Resource {
			return p.(*Fake).Snapshot()
		},
		OnNextCall: func(_ *testing.T, p provider.Provider, fn func()) {
			f := p.(*Fake)
			f.SetHook(func(context.Context, Call) {
				f.SetHook(nil)
				fn()
			})
		},
	})
}

func (s *FakeSuite) TestErrorsAreScopedToEachMethod() {
	f := new(Fake)
	ctx := s.T().Context()
	injected := errors.New("injected")
	f.FailNext(MethodSpawn, injected)
	f.SetError(MethodList, injected)
	s.Require().ErrorIs(f.Spawn(ctx, exampleSpec), injected)
	s.Empty(f.Snapshot())
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	for range 2 {
		_, err := f.List(ctx, exampleSpec.Pool)
		s.Require().ErrorIs(err, injected)
	}
	f.SetError(MethodList, nil)
	listed, err := f.List(ctx, exampleSpec.Pool)
	s.Require().NoError(err)
	s.Len(listed, 2)
}

func (s *FakeSuite) TestStartMarksOnlyAnExistingWorkspace() {
	f := new(Fake)
	ctx := s.T().Context()
	s.False(f.Started(exampleSpec.Pool, exampleSpec.Session))
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	s.False(f.Started(exampleSpec.Pool, exampleSpec.Session))
	s.Require().NoError(f.Start(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.Require().NoError(f.Start(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.True(f.Started(exampleSpec.Pool, exampleSpec.Session))
	s.Equal([]Call{
		{Method: MethodSpawn, Pool: exampleSpec.Pool, Session: exampleSpec.Session},
		{Method: MethodStart, Pool: exampleSpec.Pool, Session: exampleSpec.Session},
		{Method: MethodStart, Pool: exampleSpec.Pool, Session: exampleSpec.Session},
	}, f.Calls())
	s.Require().NoError(f.Reclaim(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.False(f.Started(exampleSpec.Pool, exampleSpec.Session))
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	s.False(f.Started(exampleSpec.Pool, exampleSpec.Session))
}

func (s *FakeSuite) TestStartFailureLeavesTheWorkspaceNotStarted() {
	f := new(Fake)
	ctx := s.T().Context()
	injected := errors.New("injected")
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	f.FailNext(MethodStart, injected)
	s.Require().ErrorIs(f.Start(ctx, exampleSpec.Pool, exampleSpec.Session), injected)
	s.False(f.Started(exampleSpec.Pool, exampleSpec.Session))
	s.Require().NoError(f.Start(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.True(f.Started(exampleSpec.Pool, exampleSpec.Session))
}

func (s *FakeSuite) TestPartialSpawnCanBeReclaimed() {
	f := new(Fake)
	ctx := s.T().Context()
	injected := errors.New("network creation failed")
	f.FailNextSpawnAfterWorkspace(injected)
	s.Require().ErrorIs(f.Spawn(ctx, exampleSpec), injected)
	partial := f.Snapshot()
	s.Require().Len(partial, 1)
	s.Equal(workspaceKind, partial[0].Kind)
	s.Equal(exampleSpec.Pool, partial[0].Labels[provider.LabelPool])
	s.Equal(exampleSpec.Session, partial[0].Labels[provider.LabelSession])
	s.Require().ErrorIs(f.Spawn(ctx, exampleSpec), provider.ErrExists)
	s.Equal(partial, f.Snapshot())
	s.Require().NoError(f.Reclaim(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.Empty(f.Snapshot())
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	spawned := f.Snapshot()
	s.Require().Len(spawned, 2)
	s.Equal(networkKind, spawned[0].Kind)
	s.Equal(workspaceKind, spawned[1].Kind)
	s.NotEqual(spawned[0].UID, spawned[1].UID)
}

func (s *FakeSuite) TestInvalidSpawnCreatesNothing() {
	f := new(Fake)
	wrong := provider.Spec{Pool: exampleSpec.Pool, Session: "p-a-b-xxxxxxxxxx", Image: exampleImage}
	s.Require().Error(f.Spawn(s.T().Context(), wrong))
	s.Empty(f.Snapshot())
}

func (s *FakeSuite) TestHookAndCallLog() {
	f := new(Fake)
	ctx := s.T().Context()
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	resource := f.Snapshot()[0]
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	f.SetHook(func(_ context.Context, call Call) {
		s.Equal(MethodDelete, call.Method)
		s.Equal(exampleSpec.Pool, call.Pool)
		s.Equal(exampleSpec.Session, call.Session)
		s.Equal(resource, call.Resource)
		s.Len(f.Calls(), 2)
		s.Len(f.Snapshot(), 2)
		call.Resource.Labels[provider.LabelPool] = "changed in hook"
		cancel()
	})
	s.Require().ErrorIs(f.Delete(canceled, exampleSpec.Pool, resource), context.Canceled)
	s.Len(f.Snapshot(), 2)
	s.Equal(exampleSpec.Pool, resource.Labels[provider.LabelPool])
	want := []Call{
		{Method: MethodSpawn, Pool: exampleSpec.Pool, Session: exampleSpec.Session},
		{
			Method:   MethodDelete,
			Pool:     exampleSpec.Pool,
			Session:  exampleSpec.Session,
			Resource: resource,
		},
	}
	calls := f.Calls()
	s.Equal(want, calls)
	calls[1].Resource.Labels[provider.LabelPool] = "changed in call copy"
	s.Equal(want, f.Calls())
}

func (s *FakeSuite) TestOperationsLeaveForeignAndUnlabelledResourcesAlone() {
	f := new(Fake)
	ctx := s.T().Context()
	foreign := provider.Resource{
		Kind: workspaceKind,
		Name: "foreign",
		UID:  "foreign-uid",
		Labels: map[string]string{
			provider.LabelPool:    "a-b",
			provider.LabelSession: exampleSpec.Session,
		},
	}
	f.Add(foreign)
	f.Add(provider.Resource{Kind: workspaceKind, Name: "unlabelled"})
	f.Add(provider.Resource{
		Kind:   workspaceKind,
		Name:   "session-only",
		Labels: map[string]string{provider.LabelSession: exampleSpec.Session},
	})
	before := f.Snapshot()
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	listed, err := f.List(ctx, exampleSpec.Pool)
	s.Require().NoError(err)
	s.Len(listed, 2)
	s.Require().NoError(f.Reclaim(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.Equal(before, f.Snapshot())
	for _, r := range before {
		s.Require().NoError(f.Delete(ctx, exampleSpec.Pool, r))
	}
	s.Equal(before, f.Snapshot())
}

func (s *FakeSuite) TestInvalidPoolIsRejectedBeforeHookAndCallLog() {
	f := new(Fake)
	ctx := s.T().Context()
	hooked := false
	f.SetHook(func(context.Context, Call) { hooked = true })
	for _, pool := range []string{"", "a_b"} {
		resource := provider.Resource{Kind: workspaceKind, Name: "x", UID: "u"}
		spec := provider.Spec{Pool: pool, Session: "p-a-xxxxxxxxxx", Image: exampleImage}
		s.ErrorIs(f.Spawn(ctx, spec), provider.ErrInvalidPool)
		s.ErrorIs(f.Reclaim(ctx, pool, "p-a-xxxxxxxxxx"), provider.ErrInvalidPool)
		_, err := f.List(ctx, pool)
		s.ErrorIs(err, provider.ErrInvalidPool)
		s.ErrorIs(f.Delete(ctx, pool, resource), provider.ErrInvalidPool)
	}
	s.False(hooked)
	s.Empty(f.Calls())
}

func (s *FakeSuite) TestCancelInsideHookStopsEveryMethod() {
	ctx := s.T().Context()
	for _, run := range []struct {
		name string
		call func(*Fake, context.Context) error
	}{
		{"spawn", func(f *Fake, c context.Context) error { return f.Spawn(c, exampleSpec) }},
		{"reclaim", func(f *Fake, c context.Context) error {
			return f.Reclaim(c, exampleSpec.Pool, exampleSpec.Session)
		}},
	} {
		f := new(Fake)
		s.Require().
			NoError(f.Spawn(ctx, provider.Spec{Pool: "a", Session: "p-a-yyyyyyyyyy", Image: exampleImage}))
		before := f.Snapshot()
		canceled, cancel := context.WithCancel(ctx)
		f.SetHook(func(context.Context, Call) { cancel() })
		s.Require().ErrorIs(run.call(f, canceled), context.Canceled, run.name)
		s.Equal(before, f.Snapshot(), run.name)
		cancel()
	}
}

func (s *FakeSuite) TestConcurrentSpawnsSeeTheSameSession() {
	f := new(Fake)
	ctx := s.T().Context()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	f.SetHook(func(context.Context, Call) {
		entered <- struct{}{}
		<-release
	})
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- f.Spawn(ctx, exampleSpec) }()
	}
	<-entered
	<-entered
	s.Empty(f.Snapshot())
	close(release)
	s.ElementsMatch([]error{nil, provider.ErrExists}, []error{<-results, <-results})
	s.Len(f.Snapshot(), 2)
}

func (s *FakeSuite) TestSnapshotCopiesArbitrarySeededResources() {
	f := new(Fake)
	invalid := provider.Resource{
		Kind: networkKind,
		Name: "z",
		UID:  "seed-uid",
		Labels: map[string]string{
			provider.LabelPool:    exampleSpec.Pool,
			provider.LabelSession: "invalid",
		},
	}
	other := provider.Resource{
		Kind: networkKind, Name: "a", Labels: map[string]string{provider.LabelPool: "a-b"},
	}
	unlabelled := provider.Resource{Kind: workspaceKind, Name: "a"}
	f.Add(invalid)
	f.Add(unlabelled)
	f.Add(other)
	invalid.Labels[provider.LabelSession] = "changed in input"
	invalid.Labels = map[string]string{
		provider.LabelPool:    exampleSpec.Pool,
		provider.LabelSession: "invalid",
	}
	snapshot := f.Snapshot()
	s.Require().Len(snapshot, 3)
	s.Require().NotEmpty(snapshot[0].UID)
	s.Require().NotEmpty(snapshot[2].UID)
	s.NotEqual(snapshot[0].UID, snapshot[2].UID)
	other.UID, unlabelled.UID = snapshot[0].UID, snapshot[2].UID
	s.Equal([]provider.Resource{other, invalid, unlabelled}, snapshot)
	snapshot[1].Labels[provider.LabelPool] = "changed in snapshot"
	listed, err := f.List(s.T().Context(), exampleSpec.Pool)
	s.Require().NoError(err)
	s.Equal([]provider.Resource{invalid}, listed)
}
