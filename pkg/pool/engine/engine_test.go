//go:build !windows

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/stretchr/testify/suite"
)

const (
	testPool    = "pool"
	testVersion = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
)

// orphanAfter is written out so a changed MaxFailures fails a test.
const orphanAfter = 5

const (
	otherPool     = "other"
	kindWorkspace = "workspace"
	kindNetwork   = "network"
)

var errBoom = errors.New("boom")

type recordedEvent struct {
	Type    string         `json:"type"`
	Session string         `json:"session"`
	Data    map[string]any `json:"data"`
}

// spyAuditor lets a test see the world at the moment an event is written.
type spyAuditor struct {
	next    Auditor
	err     error
	onWrite func()
}

func (a *spyAuditor) Write(e audit.Event) error {
	if a.onWrite != nil {
		a.onWrite()
	}
	if a.err != nil {
		return a.err
	}
	return a.next.Write(e)
}

type failingStore struct {
	*session.Store
	err error
}

func (f failingStore) Transition(
	string, session.State, session.State, string,
) (session.Session, error) {
	return session.Session{}, f.err
}

// setupPaths is how the helper store walks a new session to each state.
var setupPaths = map[session.State][]session.State{
	session.StateSpawning:   nil,
	session.StateReady:      {session.StateReady},
	session.StateBusy:       {session.StateReady, session.StateBusy},
	session.StateIdle:       {session.StateReady, session.StateBusy, session.StateIdle},
	session.StateFailed:     {session.StateFailed},
	session.StateReclaiming: {session.StateReclaiming},
	session.StateOrphaned:   {session.StateReclaiming, session.StateOrphaned},
	session.StateDeleted:    {session.StateReclaiming, session.StateDeleted},
}

type EngineSuite struct {
	suite.Suite
	store    *session.Store
	spy      *spyAuditor
	fake     *providertest.Fake
	eng      *Engine
	now      time.Time
	auditDir string
	keys     int
}

func TestEngineSuite(t *testing.T) { suite.Run(t, new(EngineSuite)) }

func (s *EngineSuite) SetupTest() {
	layout, err := session.NewLayout(filepath.Join(s.T().TempDir(), "pools"), testPool)
	s.Require().NoError(err)
	lock, err := layout.Lock()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = lock.Release() })
	lim := session.Limits{MaxConcurrent: 20, MaxQueued: 20}
	s.store, err = session.OpenStore(layout.SessionsDir(), testPool, lock, lim)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = s.store.Close() })
	s.auditDir = filepath.Join(s.T().TempDir(), "audit")
	astore, err := audit.Open(s.auditDir)
	s.Require().NoError(err)
	writer, err := audit.NewWriter(astore, audit.WriterConfig{
		Pool: testPool, Host: "h_test", PolicyVersion: testVersion, ConfigVersion: testVersion,
	})
	s.Require().NoError(err)
	s.spy = &spyAuditor{next: writer}
	s.fake = new(providertest.Fake)
	s.now = time.Now()
	s.eng = s.build(s.store)
}

func (s *EngineSuite) TestFailWritesSessionEndBeforeTheStateChanges() {
	for _, state := range []session.State{
		session.StateSpawning, session.StateReady, session.StateBusy, session.StateIdle,
	} {
		cur := s.sessionIn(state)
		s.now = cur.Created.Add(1500 * time.Millisecond)
		var seen session.State
		s.spy.onWrite = func() { seen = s.load(cur.ID).State }
		got, err := s.eng.Fail(cur.ID, EndPoolRestart)
		s.Require().NoError(err, state)
		s.Equal(state, seen, "the state still held at audit time")
		s.Equal(session.StateFailed, got.State)
		s.Equal("pool_restart", got.Reason)
		ev := s.events()[len(s.events())-1]
		s.Equal("session.end", ev.Type)
		s.Equal(cur.ID, ev.Session)
		s.Equal("pool_restart", ev.Data["devsy.session.end_reason"])
		s.InDelta(1500, ev.Data["devsy.duration_ms"], 0)
	}
}

func (s *EngineSuite) TestFailRefusesStatesThatCannotFail() {
	for _, state := range []session.State{
		session.StateQueued, session.StateFailed, session.StateReclaiming,
		session.StateOrphaned, session.StateDeleted,
	} {
		cur := s.sessionIn(state)
		_, err := s.eng.Fail(cur.ID, EndFailed)
		s.Require().ErrorIs(err, ErrNotEndable, state)
		s.Equal(cur, s.load(cur.ID))
	}
	s.Empty(s.events())
}

func (s *EngineSuite) TestBeginReclaimFromEveryLiveState() {
	for _, state := range []session.State{
		session.StateQueued, session.StateSpawning, session.StateReady,
		session.StateBusy, session.StateIdle,
	} {
		cur := s.sessionIn(state)
		before := len(s.events())
		got, err := s.eng.BeginReclaim(cur.ID, EndTTL)
		s.Require().NoError(err, state)
		s.Equal(session.StateReclaiming, got.State)
		s.Equal("ttl", got.Reason)
		s.Len(s.events(), before+1, state)
		s.Equal("session.end", s.events()[before].Type)
	}
}

func (s *EngineSuite) TestBeginReclaimAfterFailKeepsTheReasonAndWritesNothing() {
	cur := s.sessionIn(session.StateSpawning)
	_, err := s.eng.Fail(cur.ID, EndPoolRestart)
	s.Require().NoError(err)
	before := len(s.events())
	got, err := s.eng.BeginReclaim(cur.ID, EndTTL)
	s.Require().NoError(err)
	s.Equal(session.StateReclaiming, got.State)
	s.Equal("pool_restart", got.Reason)
	s.Len(s.events(), before)
}

func (s *EngineSuite) TestBeginReclaimRefusesStatesThatAlreadyEnded() {
	for _, state := range []session.State{
		session.StateReclaiming, session.StateOrphaned, session.StateDeleted,
	} {
		cur := s.sessionIn(state)
		_, err := s.eng.BeginReclaim(cur.ID, EndTTL)
		s.Require().ErrorIs(err, ErrNotEndable, state)
		s.Equal(cur, s.load(cur.ID))
	}
	s.Empty(s.events())
}

func (s *EngineSuite) TestAnUnknownEndReasonChangesNothing() {
	cur := s.sessionIn(session.StateReady)
	for _, reason := range []EndReason{"", "bogus", "Pool_Restart"} {
		_, err := s.eng.Fail(cur.ID, reason)
		s.Require().ErrorIs(err, ErrInvalidEndReason)
		_, err = s.eng.BeginReclaim(cur.ID, reason)
		s.Require().ErrorIs(err, ErrInvalidEndReason)
	}
	s.Equal(cur, s.load(cur.ID))
	s.Empty(s.events())
}

func (s *EngineSuite) TestAnAuditFailureLeavesTheSessionUnchanged() {
	cur := s.sessionIn(session.StateBusy)
	s.spy.err = errBoom
	_, err := s.eng.Fail(cur.ID, EndFailed)
	s.Require().ErrorIs(err, ErrAuditFailed)
	_, err = s.eng.BeginReclaim(cur.ID, EndTTL)
	s.Require().ErrorIs(err, ErrAuditFailed)
	s.Equal(cur, s.load(cur.ID))
}

func (s *EngineSuite) TestAStoreFailureAfterTheAuditIsReturnedAndNotHidden() {
	cur := s.sessionIn(session.StateReady)
	eng := s.build(failingStore{Store: s.store, err: errBoom})
	_, err := eng.Fail(cur.ID, EndFailed)
	s.Require().ErrorIs(err, errBoom)
	s.Equal(cur, s.load(cur.ID))
	s.Len(s.events(), 1, "the intent was recorded before the write failed")
}

func (s *EngineSuite) TestReclaimDeletesOnlyTheSessionsResources() {
	cur := s.sessionIn(session.StateReclaiming)
	other := s.sessionIn(session.StateReady)
	s.spawn(cur.ID)
	s.spawn(other.ID)
	s.fake.Add(provider.Resource{Kind: kindWorkspace, Name: "prefix", Labels: map[string]string{
		provider.LabelPool: testPool, provider.LabelSession: cur.ID + "x",
	}})
	s.fake.Add(provider.Resource{Kind: kindWorkspace, Name: "unlabelled"})
	s.now = cur.Updated
	got, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().NoError(err)
	s.Equal(session.StateDeleted, got.State)
	s.Equal(1, got.Attempts)
	s.Equal("ttl", got.Reason)
	for _, r := range s.fake.Snapshot() {
		s.NotEqual(cur.ID, r.Labels[provider.LabelSession])
	}
	s.Len(s.fake.Snapshot(), 4, "other session, prefix lookalike and unlabelled stay")
	ev := s.events()[0]
	s.Equal("session.reclaim", ev.Type)
	s.Equal(cur.ID, ev.Session)
	s.Equal("deleted", ev.Data["devsy.reclaim.result"])
	s.InDelta(1, ev.Data["devsy.reclaim.attempts"], 0)
	s.Equal(false, ev.Data["devsy.reclaim.orphaned"])
	s.NotContains(ev.Data, "devsy.reclaim.remaining")
}

func (s *EngineSuite) TestReclaimBacksOffAndPersistsEachAttempt() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.fake.SetError(providertest.MethodReclaim, errBoom)
	s.now = cur.Updated
	got, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal(1, got.Attempts)
	ev := s.events()[0]
	s.Equal("partial", ev.Data["devsy.reclaim.result"])
	s.ElementsMatch([]any{kindContainer, kindNetwork}, ev.Data["devsy.reclaim.remaining"])

	calls := s.reclaimCalls()
	_, err = s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrNotDue)
	s.Equal(calls, s.reclaimCalls(), "no provider call before the backoff ends")
	s.Equal(1, s.load(cur.ID).Attempts)

	s.now = Due(s.load(cur.ID)).Add(-time.Millisecond)
	_, err = s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrNotDue)
	s.now = Due(s.load(cur.ID))
	got, err = s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal(2, got.Attempts)
	s.Equal(2*time.Second, Backoff(got.Attempts))
}

func (s *EngineSuite) TestTheAttemptIsStoredBeforeTheProviderIsCalled() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	stored := -1
	s.fake.SetHook(func(_ context.Context, c providertest.Call) {
		if c.Method == providertest.MethodReclaim {
			stored = s.load(cur.ID).Attempts
		}
	})
	s.now = cur.Updated
	_, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().NoError(err)
	s.Equal(1, stored)
}

func (s *EngineSuite) TestTheFifthFailureOrphansAndRetriesContinue() {
	s.Equal(orphanAfter, MaxFailures)
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.fake.SetError(providertest.MethodReclaim, errBoom)
	s.now = cur.Updated
	for attempt := 1; attempt <= orphanAfter; attempt++ {
		eng := s.build(s.store)
		got, err := eng.Reclaim(context.Background(), cur.ID)
		s.Require().ErrorIs(err, ErrReclaimIncomplete)
		s.Equal(attempt, got.Attempts)
		wantState := session.StateReclaiming
		if attempt == orphanAfter {
			wantState = session.StateOrphaned
		}
		s.Equal(wantState, got.State, "attempt %d", attempt)
		s.now = Due(s.load(cur.ID))
	}
	last := s.events()[orphanAfter-1]
	s.Equal(true, last.Data["devsy.reclaim.orphaned"])
	s.Equal(false, s.events()[orphanAfter-2].Data["devsy.reclaim.orphaned"])

	got, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal(session.StateOrphaned, got.State)
	s.Equal(orphanAfter+1, got.Attempts)

	s.fake.SetError(providertest.MethodReclaim, nil)
	s.now = Due(s.load(cur.ID))
	got, err = s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().NoError(err)
	s.Equal(session.StateDeleted, got.State)
	final := s.events()[len(s.events())-1]
	s.Equal("deleted", final.Data["devsy.reclaim.result"])
	s.Equal(true, final.Data["devsy.reclaim.orphaned"])
}

func (s *EngineSuite) TestAnAuditFailureDuringReclaimKeepsTheSessionReclaiming() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.spy.err = errBoom
	s.now = cur.Updated
	got, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrAuditFailed)
	s.Equal(session.StateReclaiming, got.State)
	s.Equal(session.StateReclaiming, s.load(cur.ID).State)
	s.Equal(1, s.load(cur.ID).Attempts)
	s.Empty(s.fake.Snapshot(), "the resources are already gone")

	s.spy.err = nil
	s.now = Due(s.load(cur.ID))
	got, err = s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().NoError(err)
	s.Equal(session.StateDeleted, got.State)
	s.Equal(2, got.Attempts)
}

func (s *EngineSuite) TestAListingFailureIsAFailedAttempt() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.fake.SetError(providertest.MethodList, errBoom)
	s.now = cur.Updated
	got, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal(session.StateReclaiming, got.State)
	ev := s.events()[0]
	s.Equal("failed", ev.Data["devsy.reclaim.result"])
	s.NotContains(ev.Data, "devsy.reclaim.remaining")
}

func (s *EngineSuite) TestReclaimRefusesSessionsThatAreNotBeingReclaimed() {
	for _, state := range []session.State{
		session.StateQueued, session.StateReady, session.StateFailed, session.StateDeleted,
	} {
		cur := s.sessionIn(state)
		_, err := s.eng.Reclaim(context.Background(), cur.ID)
		s.Require().ErrorIs(err, ErrNotReclaimable, state)
		s.Equal(cur, s.load(cur.ID))
	}
	s.Empty(s.fake.Calls())
	s.Empty(s.events())
}

func (s *EngineSuite) TestACanceledContextRecordsNothing() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.now = cur.Updated
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.eng.Reclaim(ctx, cur.ID)
	s.Require().ErrorIs(err, context.Canceled)
	s.Equal(cur, s.load(cur.ID))

	ctx, cancel = context.WithCancel(context.Background())
	s.fake.SetHook(func(context.Context, providertest.Call) { cancel() })
	_, err = s.eng.Reclaim(ctx, cur.ID)
	s.Require().ErrorIs(err, context.Canceled)
	s.Equal(session.StateReclaiming, s.load(cur.ID).State)
	s.Equal(1, s.load(cur.ID).Attempts, "the attempt was stored before the call")
	s.Empty(s.events(), "an interrupted attempt writes no audit event")
}

func (s *EngineSuite) TestAClockThatSteppedBackDoesNotStallReclaim() {
	cur := s.sessionIn(session.StateReclaiming)
	s.spawn(cur.ID)
	s.fake.SetError(providertest.MethodReclaim, errBoom)
	s.now = cur.Updated
	_, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.now = s.load(cur.ID).Updated.Add(-time.Hour)
	_, err = s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal(2, s.load(cur.ID).Attempts)
}

func (s *EngineSuite) TestAnEngineRefusesASessionFromAnotherPool() {
	cur := s.sessionIn(session.StateReady)
	reclaiming := s.sessionIn(session.StateReclaiming)
	s.spawn(reclaiming.ID)
	eng, err := New(Config{
		Pool: otherPool, Store: s.store, Audit: s.spy, Provider: s.fake,
		Now: func() time.Time { return s.now },
	})
	s.Require().NoError(err)
	_, err = eng.Fail(cur.ID, EndFailed)
	s.Require().ErrorIs(err, ErrForeignPool)
	_, err = eng.BeginReclaim(cur.ID, EndTTL)
	s.Require().ErrorIs(err, ErrForeignPool)
	s.now = reclaiming.Updated
	_, err = eng.Reclaim(context.Background(), reclaiming.ID)
	s.Require().ErrorIs(err, ErrForeignPool)
	s.Equal(cur, s.load(cur.ID))
	s.Equal(reclaiming, s.load(reclaiming.ID))
	s.Empty(s.events())
	s.Len(s.fake.Snapshot(), 2, "the pool's resources stay")
	s.Len(s.fake.Calls(), 1, "only the test's own spawn")
}

func (s *EngineSuite) TestAFailedSessionReclaimsWhateverReasonIsSupplied() {
	for _, reason := range []EndReason{"", "bogus", EndTTL} {
		cur := s.sessionIn(session.StateFailed)
		got, err := s.eng.BeginReclaim(cur.ID, reason)
		s.Require().NoError(err, reason)
		s.Equal("failed", got.Reason)
	}
	s.Empty(s.events())
}

func (s *EngineSuite) TestAnEndBeforeTheSessionWasCreatedReportsZeroDuration() {
	cur := s.sessionIn(session.StateReady)
	s.now = cur.Created.Add(-time.Hour)
	_, err := s.eng.Fail(cur.ID, EndFailed)
	s.Require().NoError(err)
	s.InDelta(0, s.events()[0].Data["devsy.duration_ms"], 0)
}

func (s *EngineSuite) TestRemainingKindsAreMappedDeduplicatedAndSorted() {
	cur := s.sessionIn(session.StateReclaiming)
	for _, r := range []provider.Resource{
		{Kind: "sidecar", Name: "z-side"},
		{Kind: "gizmo", Name: "unmapped"},
		{Kind: "workspace", Name: "w1"},
		{Kind: kindNetwork, Name: "n1"},
		{Kind: "workspace", Name: "w2"},
	} {
		r.Labels = s.poolLabels(cur.ID)
		s.fake.Add(r)
	}
	s.fake.SetError(providertest.MethodReclaim, errBoom)
	s.now = cur.Updated
	_, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	s.Equal([]any{kindContainer, kindNetwork, "sidecar"},
		s.events()[0].Data["devsy.reclaim.remaining"])
}

func (s *EngineSuite) TestOnlyUnmappedKindsLeaveRemainingOut() {
	cur := s.sessionIn(session.StateReclaiming)
	s.fake.Add(provider.Resource{Kind: "gizmo", Name: "g", Labels: s.poolLabels(cur.ID)})
	s.fake.SetError(providertest.MethodReclaim, errBoom)
	s.now = cur.Updated
	_, err := s.eng.Reclaim(context.Background(), cur.ID)
	s.Require().ErrorIs(err, ErrReclaimIncomplete)
	ev := s.events()[0]
	s.Equal("partial", ev.Data["devsy.reclaim.result"])
	s.NotContains(ev.Data, "devsy.reclaim.remaining")
}

func (s *EngineSuite) poolLabels(id string) map[string]string {
	labels := map[string]string{provider.LabelPool: testPool}
	if id != "" {
		labels[provider.LabelSession] = id
	}
	return labels
}

func (s *EngineSuite) build(store Store) *Engine {
	eng, err := New(Config{
		Pool: testPool, Store: store, Audit: s.spy, Provider: s.fake,
		Now: func() time.Time { return s.now },
	})
	s.Require().NoError(err)
	return eng
}

func (s *EngineSuite) sessionIn(state session.State) session.Session {
	s.T().Helper()
	s.keys++
	adm, err := s.store.Admit(fmt.Sprintf("key-%d", s.keys))
	s.Require().NoError(err)
	cur := adm.Session
	if state == session.StateQueued {
		return cur
	}
	cur, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.Require().True(ok)
	for _, next := range setupPaths[state] {
		reason := ""
		switch next {
		case session.StateFailed:
			reason = string(EndFailed)
		case session.StateReclaiming:
			reason = string(EndTTL)
		}
		cur, err = s.store.Transition(cur.ID, cur.State, next, reason)
		s.Require().NoError(err)
	}
	return cur
}

func (s *EngineSuite) events() []recordedEvent {
	s.T().Helper()
	reader, err := audit.OpenReader(s.auditDir)
	s.Require().NoError(err)
	var out []recordedEvent
	err = reader.Scan(audit.ReaderFilter{}, func(line []byte) error {
		var ev recordedEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return err
		}
		out = append(out, ev)
		return nil
	}, nil)
	s.Require().NoError(err)
	return out
}

func (s *EngineSuite) load(id string) session.Session {
	s.T().Helper()
	got, err := s.store.Load(id)
	s.Require().NoError(err)
	return got
}

func (s *EngineSuite) spawn(id string) {
	s.T().Helper()
	spec := provider.Spec{Pool: testPool, Session: id}
	s.Require().NoError(s.fake.Spawn(context.Background(), spec))
}

func (s *EngineSuite) reclaimCalls() int {
	n := 0
	for _, c := range s.fake.Calls() {
		if c.Method == providertest.MethodReclaim {
			n++
		}
	}
	return n
}
