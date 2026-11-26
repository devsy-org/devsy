//go:build !windows

package engine

import (
	"context"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

// restartStates is every state a restart can find, with the state it must
// leave each session in once Reconcile returns.
var restartStates = []struct {
	from session.State
	want session.State
}{
	{session.StateSpawning, session.StateReclaiming},
	{session.StateReady, session.StateReclaiming},
	{session.StateBusy, session.StateReclaiming},
	{session.StateIdle, session.StateReclaiming},
	{session.StateFailed, session.StateReclaiming},
	{session.StateReclaiming, session.StateReclaiming},
	{session.StateOrphaned, session.StateOrphaned},
	{session.StateDeleted, session.StateDeleted},
	{session.StateQueued, session.StateQueued},
}

func (s *EngineSuite) seedRestart() map[string]session.State {
	s.T().Helper()
	want := make(map[string]session.State)
	for _, c := range restartStates {
		cur := s.sessionIn(c.from)
		want[cur.ID] = c.want
	}
	return want
}

func (s *EngineSuite) states() map[string]session.State {
	s.T().Helper()
	all, err := s.store.List()
	s.Require().NoError(err)
	got := make(map[string]session.State)
	for _, cur := range all {
		got[cur.ID] = cur.State
	}
	return got
}

func (s *EngineSuite) workspace(name string, labels map[string]string) provider.Resource {
	return provider.Resource{Kind: kindWorkspace, Name: name, Labels: labels}
}

func (s *EngineSuite) TestReconcileEndsWhatTheRestartCutOff() {
	want := s.seedRestart()
	report, err := s.eng.Reconcile(context.Background())
	s.Require().NoError(err)
	s.Equal(Report{Sessions: 9, Failed: 3}, report)
	s.Equal(want, s.states())

	events := s.events()
	s.Equal("pool.start", events[0].Type)
	s.Empty(events[0].Session)
	s.InDelta(9, events[0].Data["devsy.pool.sessions_loaded"], 0)
	s.InDelta(3, events[0].Data["devsy.pool.sessions_failed"], 0)
	s.InDelta(0, events[0].Data["devsy.pool.orphans_found"], 0)
	ends := 0
	for _, ev := range events[1:] {
		s.Equal("session.end", ev.Type)
		s.Equal("pool_restart", ev.Data["devsy.session.end_reason"])
		ends++
	}
	s.Equal(4, ends, "spawning, ready, busy and idle; failed already ended")
	for id, state := range want {
		if state == session.StateReclaiming && s.load(id).Reason == "pool_restart" {
			continue
		}
		s.NotEqual("pool_restart", s.load(id).Reason, id)
	}
}

func (s *EngineSuite) TestReconcileTwiceChangesNothingTheSecondTime() {
	s.seedRestart()
	_, err := s.eng.Reconcile(context.Background())
	s.Require().NoError(err)
	before, count := s.states(), len(s.events())
	report, err := s.eng.Reconcile(context.Background())
	s.Require().NoError(err)
	s.Equal(Report{Sessions: 9}, report)
	s.Equal(before, s.states())
	s.Len(s.events(), count+1, "only the second pool.start")
}

func (s *EngineSuite) TestReconcileWritesNothingWhenPoolStartCannotBeAudited() {
	s.seedRestart()
	before := s.states()
	s.spy.err = errBoom
	_, err := s.eng.Reconcile(context.Background())
	s.Require().ErrorIs(err, ErrAuditFailed)
	s.Equal(before, s.states())
	s.Empty(s.fake.Calls()[1:], "only the listing ran")
}

func (s *EngineSuite) TestReconcileStopsWhenASessionEventCannotBeAudited() {
	cur := s.sessionIn(session.StateBusy)
	writes := 0
	s.spy.onWrite = func() {
		writes++
		if writes == 2 {
			s.spy.err = errBoom
		}
	}
	_, err := s.eng.Reconcile(context.Background())
	s.Require().ErrorIs(err, ErrAuditFailed)
	s.Equal(session.StateBusy, s.load(cur.ID).State)
}

func (s *EngineSuite) TestReconcileReclaimsResourcesWithoutALiveRecord() {
	done := s.sessionIn(session.StateDeleted)
	live := s.sessionIn(session.StateQueued)
	gone, err := session.NewID(testPool)
	s.Require().NoError(err)
	s.spawn(gone)
	s.spawn(done.ID)
	s.spawn(live.ID)
	s.fake.Add(s.workspace("no-session", s.poolLabels("")))
	s.fake.Add(s.workspace("lookalike", s.poolLabels(live.ID+"x")))
	s.fake.Add(s.workspace("unlabelled", nil))
	s.fake.Add(s.workspace("foreign", map[string]string{
		provider.LabelPool: otherPool, provider.LabelSession: gone,
	}))

	report, err := s.eng.Reconcile(context.Background())
	s.Require().NoError(err)
	s.Equal(6, report.Orphans, "two resources each for two sessions and two stray objects")
	var left []string
	for _, r := range s.fake.Snapshot() {
		left = append(left, r.Name)
	}
	s.ElementsMatch([]string{
		"unlabelled", "foreign",
		s.nameOf(live.ID, kindWorkspace), s.nameOf(live.ID, kindNetwork),
	}, left)

	reclaims := map[string]recordedEvent{}
	for _, ev := range s.events() {
		if ev.Type == "session.reclaim" {
			reclaims[ev.Session] = ev
		}
	}
	s.Len(reclaims, 2)
	for _, id := range []string{gone, done.ID} {
		s.Equal("deleted", reclaims[id].Data["devsy.reclaim.result"])
		s.Equal(true, reclaims[id].Data["devsy.reclaim.orphaned"])
		s.InDelta(1, reclaims[id].Data["devsy.reclaim.attempts"], 0)
	}
	deletes := 0
	for _, c := range s.fake.Calls() {
		if c.Method == providertest.MethodDelete {
			s.NotEmpty(c.Resource.UID)
			deletes++
		}
	}
	s.Equal(2, deletes)
}

func (s *EngineSuite) nameOf(id, kind string) string {
	s.T().Helper()
	for _, r := range s.fake.Snapshot() {
		if r.Kind == kind && r.Labels[provider.LabelSession] == id {
			return r.Name
		}
	}
	s.FailNow("no resource", "%s %s", kind, id)
	return ""
}

func (s *EngineSuite) TestReconcileReportsAnOrphanThatCannotBeRemoved() {
	cur := s.sessionIn(session.StateReady)
	s.fake.Add(s.workspace("no-session", s.poolLabels("")))
	s.fake.SetError(providertest.MethodDelete, errBoom)
	_, err := s.eng.Reconcile(context.Background())
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal(session.StateReclaiming, s.load(cur.ID).State, "the sessions are still ended")
}

func (s *EngineSuite) TestReconcileChangesNothingWhenTheListingFails() {
	s.seedRestart()
	before := s.states()
	s.fake.SetError(providertest.MethodList, errBoom)
	_, err := s.eng.Reconcile(context.Background())
	s.Require().ErrorIs(err, errBoom)
	s.Equal(before, s.states())
	s.Empty(s.events())

	s.fake.SetError(providertest.MethodList, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.eng.Reconcile(ctx)
	s.Require().ErrorIs(err, context.Canceled)
	s.Equal(before, s.states())
}

func (s *EngineSuite) TestReconcileRefusesSessionsFromAnotherPool() {
	cur := s.sessionIn(session.StateBusy)
	s.spawn(cur.ID)
	s.fake.Add(s.workspace("no-session", s.poolLabels("")))
	eng, err := New(Config{
		Pool: otherPool, Store: s.store, Audit: s.spy, Provider: s.fake,
		Now: func() time.Time { return s.now },
	})
	s.Require().NoError(err)
	_, err = eng.Reconcile(context.Background())
	s.Require().ErrorIs(err, ErrForeignPool)
	s.Equal(session.StateBusy, s.load(cur.ID).State)
	s.Empty(s.events())
	s.Len(s.fake.Snapshot(), 3)
}

func (s *EngineSuite) TestSweepReclaimsDueSessionsAndReportsTheNextWake() {
	due := s.sessionIn(session.StateReclaiming)
	retry := s.sessionIn(session.StateOrphaned)
	live := s.sessionIn(session.StateReady)
	for _, id := range []string{due.ID, retry.ID, live.ID} {
		s.spawn(id)
	}
	s.now = retry.Updated
	s.fake.SetError(providertest.MethodReclaim, errBoom)
	_, err := s.eng.Reclaim(context.Background(), retry.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.fake.SetError(providertest.MethodReclaim, nil)

	next, err := s.eng.Sweep(context.Background())
	s.Require().NoError(err)
	s.Equal(session.StateDeleted, s.load(due.ID).State)
	s.Equal(session.StateOrphaned, s.load(retry.ID).State, "not due yet")
	s.Equal(session.StateReady, s.load(live.ID).State)
	s.True(next.Equal(Due(s.load(retry.ID))), "next=%v due=%v", next, Due(s.load(retry.ID)))

	s.now = next
	next, err = s.eng.Sweep(context.Background())
	s.Require().NoError(err)
	s.Equal(session.StateDeleted, s.load(retry.ID).State)
	s.True(next.IsZero())
}

func (s *EngineSuite) TestSweepStopsOnAnAuditFailure() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.now = cur.Updated
	s.spy.err = errBoom
	_, err := s.eng.Sweep(context.Background())
	s.Require().ErrorIs(err, ErrAuditFailed)
	s.Equal(session.StateReclaiming, s.load(cur.ID).State)
}

func (s *EngineSuite) TestAnInterruptedPoolEndsWithNoSessionLeftBehind() {
	want := s.seedRestart()
	for id := range want {
		s.spawn(id)
	}
	_, err := s.eng.Reconcile(context.Background())
	s.Require().NoError(err)
	for range 12 {
		s.now = s.now.Add(10 * time.Minute)
		_, err = s.eng.Sweep(context.Background())
		s.Require().NoError(err)
	}
	for id, state := range s.states() {
		if want[id] == session.StateQueued {
			s.Equal(session.StateQueued, state)
			continue
		}
		s.Equal(session.StateDeleted, state, id)
	}
	for _, r := range s.fake.Snapshot() {
		owner := want[r.Labels[provider.LabelSession]]
		s.Equal(session.StateQueued, owner, "only a queued session may hold resources")
	}
}
