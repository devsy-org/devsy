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
)

const canaryKey = "private-key-canary-f50d"

func (s *StoreSuite) TestAdmitReusesKeyAndStoresOnlyItsHash() {
	lim := Limits{MaxConcurrent: 1, MaxQueued: 1}
	first := s.admit(canaryKey, lim)
	other := s.admit("other key", lim)
	s.False(first.Existing)
	s.False(other.Existing)
	s.NotEqual(first.Session.ID, other.Session.ID)
	retry := s.admit(canaryKey, lim)
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
	lim := Limits{MaxConcurrent: 8}
	before := s.poolSnapshot()
	for _, key := range []string{
		"", canaryKey + "\n", canaryKey + "\u0085", canaryKey + "\xff", strings.Repeat(canaryKey, 30),
	} {
		got, err := s.store.Admit(key, lim)
		s.Require().ErrorIs(err, ErrInvalidKey)
		s.Zero(got)
		s.NotContains(err.Error(), canaryKey)
	}
	s.Equal(before, s.poolSnapshot())
	for _, key := range []string{"x", strings.Repeat("\u00e9", 128)} {
		s.False(s.admit(key, lim).Existing)
	}
}

func (s *StoreSuite) TestAdmitAndPromoteValidateLimits() {
	before := s.poolSnapshot()
	for _, lim := range []Limits{{}, {MaxConcurrent: -1}, {MaxConcurrent: 1, MaxQueued: -1}} {
		got, err := s.store.Admit(canaryKey, lim)
		s.Require().ErrorIs(err, errInvalidLimits)
		s.Zero(got)
		next, ok, err := s.store.Promote(lim)
		s.Require().ErrorIs(err, errInvalidLimits)
		s.Zero(next)
		s.False(ok)
	}
	s.Equal(before, s.poolSnapshot())
	s.False(s.admit(canaryKey, Limits{MaxConcurrent: math.MaxInt, MaxQueued: math.MaxInt}).Existing)
}

func (s *StoreSuite) TestAdmitCapacityCountsFailedAndOrphanedUntilDeleted() {
	lim := Limits{MaxConcurrent: 2, MaxQueued: 1}
	first := s.admit("first", lim).Session
	started, ok, err := s.store.Promote(lim)
	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal(first.ID, started.ID)
	s.move(first.ID, StateSpawning, StateFailed)
	second := s.admit("second", lim).Session
	s.move(second.ID, StateQueued, StateReclaiming, StateOrphaned)
	s.admit("third", lim)

	for _, state := range []State{StateFailed, StateReclaiming, StateOrphaned} {
		before := s.poolSnapshot()
		got, err := s.store.Admit(canaryKey, lim)
		s.Require().ErrorIs(err, ErrPoolFull, state)
		s.Zero(got)
		s.NotContains(err.Error(), canaryKey)
		s.Equal(before, s.poolSnapshot())
		if state == StateFailed {
			s.move(first.ID, StateFailed, StateReclaiming)
		}
	}
	s.move(first.ID, StateReclaiming, StateDeleted)
	s.False(s.admit(canaryKey, lim).Existing)
	s.move(second.ID, StateOrphaned, StateDeleted)
	s.False(s.admit("fifth", lim).Existing)
}

func (s *StoreSuite) TestTransitionCannotBypassPromotionLimit() {
	lim := Limits{MaxConcurrent: 1, MaxQueued: 1}
	first := s.admit("first", lim).Session
	s.clock = s.clock.Add(time.Second)
	waiting := s.admit("waiting", lim).Session
	before := s.snapshot()
	for range 5 {
		next, err := s.store.Transition(waiting.ID, StateQueued, StateSpawning, "")
		s.Require().ErrorIs(err, ErrUsePromote)
		s.Zero(next)
		s.Equal(before, s.snapshot())
	}

	started, ok, err := s.store.Promote(lim)
	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal(first.ID, started.ID)
	s.Equal(StateSpawning, started.State)
	next, ok, err := s.store.Promote(lim)
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

func (s *StoreSuite) TestPromoteUsesCreatedThenIDAndRespectsActiveLimit() {
	lim := Limits{MaxConcurrent: 1, MaxQueued: 3}
	s.clock = s.clock.Add(time.Minute)
	useSource(s.T(), fixedReader{0x00})
	newer := s.admit("newer", lim).Session
	s.clock = s.clock.Add(-time.Minute)
	useSource(s.T(), fixedReader{0x80})
	tieLater := s.admit("tie later", lim).Session
	useSource(s.T(), fixedReader{0x40})
	tieFirst := s.admit("tie first", lim).Session
	s.clock = s.clock.Add(time.Hour)
	for _, want := range []Session{tieFirst, tieLater, newer} {
		next, ok, err := s.store.Promote(lim)
		s.Require().NoError(err)
		s.True(ok)
		s.Equal(want.ID, next.ID)
		s.Equal(StateSpawning, next.State)
		s.Empty(next.Reason)
		s.Equal(s.clock, next.Updated)
		before := s.poolSnapshot()
		next, ok, err = s.store.Promote(lim)
		s.Require().NoError(err)
		s.False(ok)
		s.Zero(next)
		s.Equal(before, s.poolSnapshot())
		s.move(want.ID, StateSpawning, StateReclaiming, StateDeleted)
	}
	next, ok, err := s.store.Promote(lim)
	s.Require().NoError(err)
	s.False(ok)
	s.Zero(next)
}

func (s *StoreSuite) TestPromoteReturnsZeroSessionOnPublishFailure() {
	s.create()
	before := s.poolSnapshot()
	s.stubRename(func(*os.Root, string, string) error { return errors.New("disk full") })
	next, ok, err := s.store.Promote(Limits{MaxConcurrent: 1})
	s.Require().ErrorContains(err, "disk full")
	s.Zero(next)
	s.False(ok)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestConcurrentAdmitDoesNotOverfillPool() {
	const n = 32
	lim := Limits{MaxConcurrent: 2, MaxQueued: 1}
	results := make(chan error, n)
	start := make(chan struct{})
	for i := range n {
		go func() {
			<-start
			_, err := s.store.Admit(fmt.Sprintf("key-%d", i), lim)
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

func (s *StoreSuite) admit(key string, lim Limits) Admission {
	got, err := s.store.Admit(key, lim)
	s.Require().NoError(err)
	return got
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
