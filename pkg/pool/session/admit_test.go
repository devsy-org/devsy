//go:build !windows

package session

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const canaryKey = "private-key-canary-f50d"

func (s *StoreSuite) TestAdmitReusesKeyAndStoresOnlyItsHash() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 1})
	first := s.admit(canaryKey)
	other := s.admit("other key")
	s.False(first.Existing)
	s.False(other.Existing)
	s.NotEqual(first.Session.ID, other.Session.ID)
	retry := s.admit(canaryKey)
	s.Equal(Admission{Session: first.Session, Existing: true}, retry)
	list, err := s.store.List()
	s.Require().NoError(err)
	s.Len(list, 2)
	s.Contains(dirNames(s, s.layout.KeysDir()), keyFile(canaryKey))
	dir, err := os.Stat(s.layout.KeysDir())
	s.Require().NoError(err)
	s.Equal(fs.FileMode(dirMode), dir.Mode().Perm())
	file, err := s.store.keys.Stat(keyFile(canaryKey))
	s.Require().NoError(err)
	s.Equal(fs.FileMode(fileMode), file.Mode().Perm())
	tree := s.poolSnapshot()
	s.Equal(s.keyJSON(keyDigest(canaryKey), first.Session.ID, first.Session.Created),
		tree[keysName+"/"+keyFile(canaryKey)])
	for name, data := range tree {
		s.NotContains(name, canaryKey)
		s.NotContains(data, canaryKey)
	}
}

func (s *StoreSuite) TestAdmitRefusesInvalidKeysWithoutEchoingThem() {
	s.store = s.openWith(Limits{MaxConcurrent: 8})
	before := s.poolSnapshot()
	for _, key := range []string{
		"", canaryKey + "\n", canaryKey + "\u0085", canaryKey + "\xff", strings.Repeat(canaryKey, 30),
	} {
		got, err := s.store.Admit(key)
		s.Require().ErrorIs(err, ErrInvalidKey)
		s.Zero(got)
		s.NotContains(err.Error(), canaryKey)
	}
	s.Equal(before, s.poolSnapshot())
	for _, key := range []string{"x", strings.Repeat("\u00e9", 128)} {
		s.False(s.admit(key).Existing)
	}
}

func (s *StoreSuite) TestOpenStoreValidatesLimitsBeforePreparingDirectories() {
	s.writeFile(".tmp-1", "keep on refusal")
	s.Require().NoError(os.Remove(s.layout.KeysDir()))
	before := s.poolSnapshot()
	for _, lim := range []Limits{{}, {MaxConcurrent: -1}, {MaxConcurrent: 1, MaxQueued: -1}} {
		got, err := OpenStore(s.dir, storePool, s.lock, lim)
		s.Require().ErrorIs(err, errInvalidLimits)
		s.Nil(got)
	}
	_, err := OpenStore(s.dir, "Bad Pool", nil, Limits{})
	s.Require().ErrorIs(err, errInvalidPool)
	_, err = OpenStore(s.dir, storePool, nil, Limits{})
	s.Require().ErrorIs(err, errLockNotHeld)
	s.Equal(before, s.poolSnapshot())

	s.store = s.openWith(Limits{MaxConcurrent: math.MaxInt, MaxQueued: math.MaxInt})
	s.False(s.admit(canaryKey).Existing)
}

func (s *StoreSuite) TestLimitsFromUsesOnlyAdmissionLimits() {
	got := LimitsFrom(poolconfig.Capacity{MaxConcurrent: 3, MaxQueued: 4, Warm: 2})
	s.Equal(Limits{MaxConcurrent: 3, MaxQueued: 4}, got)
}

func (s *StoreSuite) TestAdmitReleasesCapacityWhenUnstartedSessionLeavesQueue() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 1})
	s.admit("first")
	s.promote()
	waiting := s.admit("waiting").Session
	s.assertPoolFull()
	reclaiming := s.move(waiting.ID, StateQueued, StateReclaiming)
	s.False(reclaiming.Started)
	s.False(s.admit(canaryKey).Existing)
}

func (s *StoreSuite) TestAdmitCapacityCountsStartedFailedReclaimingAndOrphanedUntilDeleted() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 1})
	first := s.admit("first").Session
	s.Equal(first.ID, s.promote().ID)
	s.admit("waiting")

	states := []State{StateSpawning, StateFailed, StateReclaiming, StateOrphaned, StateDeleted}
	for i, to := range states[1:] {
		s.assertPoolFull()
		next := s.move(first.ID, states[i], to)
		s.True(next.Started)
	}
	s.False(s.admit(canaryKey).Existing)
}

func (s *StoreSuite) TestLegacyStartedStatesHoldCapacityWithoutStartedFlag() {
	s.store = s.openWith(Limits{MaxConcurrent: 4, MaxQueued: 1})
	for i, state := range []State{StateSpawning, StateReady, StateBusy, StateIdle} {
		legacy := validSession()
		legacy.ID = "p-pool-" + strings.Repeat(string(rune('a'+i)), idSuffixLen)
		legacy.State = state
		s.writeSession(legacy)
	}
	useSource(s.T(), fixedReader{0xff})
	queued := s.admit(canaryKey).Session
	s.Equal(StateQueued, queued.State)
	next, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.False(ok)
	s.Zero(next)
}

func (s *StoreSuite) TestLegacyStartedSessionKeepsSlotWhenReclaimed() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 1})
	legacy := validSession()
	legacy.State = StateSpawning
	s.writeSession(legacy)
	s.admit("waiting")

	reclaiming := s.move(legacy.ID, StateSpawning, StateReclaiming)
	s.True(reclaiming.Started)
	s.assertPoolFull()
	next, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.False(ok)
	s.Zero(next)
}

func (s *StoreSuite) TestLegacyStartedStatesKeepSlotWhenReclaimedOrFailed() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 1})
	s.admit("waiting")
	for i, from := range []State{StateReady, StateBusy, StateIdle} {
		for j, to := range []State{StateReclaiming, StateFailed} {
			legacy := validSession()
			legacy.ID = "p-pool-" + strings.Repeat(string(rune('a'+2*i+j)), idSuffixLen)
			legacy.State = from
			s.writeSession(legacy)

			moved := s.move(legacy.ID, from, to)
			s.True(moved.Started, "%s to %s", from, to)
			s.assertPoolFull()
			next, ok, err := s.store.Promote()
			s.Require().NoError(err)
			s.False(ok, "%s to %s", from, to)
			s.Zero(next)

			path := []State{StateReclaiming, StateDeleted}
			if to == StateFailed {
				path = []State{StateFailed, StateReclaiming, StateDeleted}
			}
			s.move(legacy.ID, path...)
		}
	}
}

func (s *StoreSuite) TestLegacyReclaimingSessionDoesNotHoldCapacity() {
	legacy := validSession()
	legacy.State = StateReclaiming
	s.writeSession(legacy)
	useSource(s.T(), fixedReader{0xff})
	queued := s.admit(canaryKey).Session
	s.Equal(queued.ID, s.promote().ID)
}

func (s *StoreSuite) assertPoolFull() {
	before := s.poolSnapshot()
	got, err := s.store.Admit(canaryKey)
	s.Require().ErrorIs(err, ErrPoolFull)
	s.Zero(got)
	s.NotContains(err.Error(), canaryKey)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestTransitionCannotBypassPromotionLimit() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 1})
	first := s.admit("first").Session
	s.clock = s.clock.Add(time.Second)
	waiting := s.admit("waiting").Session
	before := s.snapshot()
	for range 5 {
		next, err := s.store.Transition(waiting.ID, StateQueued, StateSpawning, "")
		s.Require().ErrorIs(err, ErrUsePromote)
		s.Zero(next)
		s.Equal(before, s.snapshot())
	}

	started, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal(first.ID, started.ID)
	s.Equal(StateSpawning, started.State)
	next, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.False(ok)
	s.Zero(next)
	got, err := s.store.Load(waiting.ID)
	s.Require().NoError(err)
	s.Equal(waiting, got)
}

func (s *StoreSuite) TestStoreDoesNotExposeCapacityFreeCreation() {
	_, exported := reflect.TypeFor[*Store]().MethodByName("Create")
	s.False(exported)
}

func (s *StoreSuite) TestTransitionRefusesStartingWithoutCheckingTheLock() {
	sess := s.create()
	s.Require().NoError(s.lock.Release())
	next, err := s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().ErrorIs(err, ErrUsePromote)
	s.Zero(next)
}

func (s *StoreSuite) TestPromoteUsesSequenceWhenClockMovesBackward() {
	lim := Limits{MaxConcurrent: 1, MaxQueued: 1}
	s.store = s.openWith(lim)
	useSource(s.T(), fixedReader{0x00})
	first := s.admit("first").Session
	s.clock = s.clock.Add(-time.Minute)
	s.Require().NoError(s.store.Close())
	s.store = s.openWith(lim)
	useSource(s.T(), fixedReader{0xff})
	second := s.admit("second").Session
	s.Equal(uint64(1), first.Seq)
	s.Equal(uint64(2), second.Seq)
	s.True(second.Created.Before(first.Created))
	s.Equal(first.ID, s.promote().ID)
}

func (s *StoreSuite) TestPromoteUsesCreatedAndIDForLegacyRecordsBeforeSequencedRecords() {
	s.store = s.openWith(Limits{MaxConcurrent: 1, MaxQueued: 3})
	useSource(s.T(), fixedReader{0xff})
	sequenced := s.admit("sequenced").Session
	newer, tieFirst, tieLater := validSession(), validSession(), validSession()
	newer.Created = newer.Created.Add(time.Minute)
	tieFirst.ID = "p-pool-bbbbbbbbbb"
	tieLater.ID = "p-pool-cccccccccc"
	for _, sess := range []Session{newer, tieLater, tieFirst} {
		s.writeSession(sess)
	}
	s.clock = s.clock.Add(time.Hour)
	for _, want := range []Session{tieFirst, tieLater, newer, sequenced} {
		next := s.promote()
		s.Equal(want.ID, next.ID)
		s.Equal(StateSpawning, next.State)
		s.Empty(next.Reason)
		s.Equal(s.clock, next.Updated)
		before := s.poolSnapshot()
		next, ok, err := s.store.Promote()
		s.Require().NoError(err)
		s.False(ok)
		s.Zero(next)
		s.Equal(before, s.poolSnapshot())
		s.move(want.ID, StateSpawning, StateReclaiming, StateDeleted)
	}
	next, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.False(ok)
	s.Zero(next)
}

func (s *StoreSuite) TestAdmitRefusesSequenceOverflowWithoutWriting() {
	exhausted := validSession()
	exhausted.State, exhausted.Seq = StateDeleted, math.MaxUint64
	s.writeSession(exhausted)
	before := s.poolSnapshot()
	got, err := s.store.Admit(canaryKey)
	s.Require().ErrorIs(err, errSeqExhausted)
	s.Zero(got)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestPromoteReturnsZeroSessionOnPublishFailure() {
	s.create()
	before := s.poolSnapshot()
	s.stubRename(func(*os.Root, string, string) error { return errors.New("disk full") })
	next, ok, err := s.store.Promote()
	s.Require().ErrorContains(err, "disk full")
	s.Zero(next)
	s.False(ok)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestConcurrentAdmitDoesNotOverfillPool() {
	const n = 32
	s.store = s.openWith(Limits{MaxConcurrent: 2, MaxQueued: 1})
	results := make(chan error, n)
	start := make(chan struct{})
	for i := range n {
		go func() {
			<-start
			_, err := s.store.Admit(fmt.Sprintf("key-%d", i))
			results <- err
		}()
	}
	close(start)
	var admitted int
	for range n {
		err := s.receive(results, "admission did not finish")
		if err == nil {
			admitted++
		} else {
			s.ErrorIs(err, ErrPoolFull)
		}
	}
	s.Equal(3, admitted)
	list, err := s.store.List()
	s.Require().NoError(err)
	s.Len(list, 3)
	s.Len(dirNames(s, s.layout.KeysDir()), 3)
}

func (s *StoreSuite) admit(key string) Admission {
	got, err := s.store.Admit(key)
	s.Require().NoError(err)
	return got
}

func (s *StoreSuite) promote() Session {
	next, ok, err := s.store.Promote()
	s.Require().NoError(err)
	s.Require().True(ok)
	s.True(next.Started)
	return next
}

func (s *StoreSuite) writeSession(sess Session) {
	data, err := encode(sess)
	s.Require().NoError(err)
	s.writeFile(fileName(sess.ID), string(data))
}

func (s *StoreSuite) move(id string, states ...State) Session {
	var next Session
	for i, to := range states[1:] {
		var err error
		next, err = s.store.Transition(id, states[i], to, "")
		s.Require().NoError(err)
	}
	return next
}

func (s *StoreSuite) poolSnapshot() map[string]string {
	root := s.lock.root.FS()
	out := map[string]string{}
	err := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[path] = "<dir>"
			return nil
		}
		data, err := fs.ReadFile(root, path)
		out[path] = string(data)
		return err
	})
	s.Require().NoError(err)
	return out
}
