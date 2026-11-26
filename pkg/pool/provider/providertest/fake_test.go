package providertest

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/stretchr/testify/suite"
)

var exampleSpec = provider.Spec{Pool: "a", Session: "p-a-xxxxxxxxxx"}

type FakeSuite struct{ suite.Suite }

func TestFakeSuite(t *testing.T) { suite.Run(t, new(FakeSuite)) }

func TestFakeContract(t *testing.T) {
	RunContract(t, func(*testing.T) provider.Provider { return new(Fake) })
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

func (s *FakeSuite) TestPartialSpawnCanBeReclaimed() {
	f := new(Fake)
	ctx := s.T().Context()
	injected := errors.New("network creation failed")
	f.FailNextSpawnAfterWorkspace(injected)
	s.Require().ErrorIs(f.Spawn(ctx, exampleSpec), injected)
	partial := f.Snapshot()
	s.Require().Len(partial, 1)
	s.Equal("workspace", partial[0].Kind)
	s.Equal(exampleSpec.Pool, partial[0].Labels[provider.LabelPool])
	s.Equal(exampleSpec.Session, partial[0].Labels[provider.LabelSession])
	s.Require().ErrorIs(f.Spawn(ctx, exampleSpec), provider.ErrExists)
	s.Equal(partial, f.Snapshot())
	s.Require().NoError(f.Reclaim(ctx, exampleSpec.Pool, exampleSpec.Session))
	s.Empty(f.Snapshot())
	s.Require().NoError(f.Spawn(ctx, exampleSpec))
	spawned := f.Snapshot()
	s.Require().Len(spawned, 2)
	s.Equal("network", spawned[0].Kind)
	s.Equal("workspace", spawned[1].Kind)
}

func (s *FakeSuite) TestInvalidSpawnCreatesNothing() {
	f := new(Fake)
	wrong := provider.Spec{Pool: exampleSpec.Pool, Session: "p-a-b-xxxxxxxxxx"}
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
	s.Require().ErrorIs(f.Delete(canceled, resource), context.Canceled)
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
		Kind: "network",
		Name: "z",
		Labels: map[string]string{
			provider.LabelPool:    exampleSpec.Pool,
			provider.LabelSession: "invalid",
		},
	}
	other := provider.Resource{
		Kind: "network", Name: "a", Labels: map[string]string{provider.LabelPool: "a-b"},
	}
	unlabelled := provider.Resource{Kind: "workspace", Name: "a"}
	f.Add(invalid)
	f.Add(unlabelled)
	f.Add(other)
	invalid.Labels[provider.LabelSession] = "changed in input"
	invalid.Labels = map[string]string{
		provider.LabelPool:    exampleSpec.Pool,
		provider.LabelSession: "invalid",
	}
	snapshot := f.Snapshot()
	s.Equal([]provider.Resource{other, invalid, unlabelled}, snapshot)
	snapshot[1].Labels[provider.LabelPool] = "changed in snapshot"
	listed, err := f.List(s.T().Context(), exampleSpec.Pool)
	s.Require().NoError(err)
	s.Equal([]provider.Resource{invalid}, listed)
}
