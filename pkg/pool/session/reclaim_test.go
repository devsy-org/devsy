//go:build !windows

package session

import (
	"os"
	"time"
)

func (s *StoreSuite) TestCountReclaimAttemptPersistsAcrossReopenAndTransitions() {
	sess := s.create()
	s.move(sess.ID, StateQueued, StateReclaiming)
	s.clock = s.clock.Add(time.Minute)
	first, err := s.store.CountReclaimAttempt(sess.ID)
	s.Require().NoError(err)
	s.Equal(1, first.Attempts)
	s.Equal(s.clock, first.Updated)
	s.Equal(sess.Created, first.Created)
	s.Equal(StateReclaiming, first.State)
	s.Require().NoError(s.store.Close())
	s.store = s.open()
	got, err := s.store.Load(sess.ID)
	s.Require().NoError(err)
	s.Equal(first, got)

	orphaned := s.move(sess.ID, StateReclaiming, StateOrphaned)
	s.Equal(first.Attempts, orphaned.Attempts)
	second, err := s.store.CountReclaimAttempt(sess.ID)
	s.Require().NoError(err)
	s.Equal(2, second.Attempts)
	s.Equal(StateOrphaned, second.State)
	retrying := s.move(sess.ID, StateOrphaned, StateReclaiming)
	s.Equal(second.Attempts, retrying.Attempts)
	deleted := s.move(sess.ID, StateReclaiming, StateDeleted)
	s.Equal(second.Attempts, deleted.Attempts)
}

func (s *StoreSuite) TestCountReclaimAttemptRefusesOtherStatesWithoutWriting() {
	sess := s.create()
	s.assertAttemptRefused(sess.ID, ErrStateConflict)
	s.promote()
	s.assertAttemptRefused(sess.ID, ErrStateConflict)
	s.move(sess.ID, StateSpawning, StateReady)
	s.assertAttemptRefused(sess.ID, ErrStateConflict)
	s.move(sess.ID, StateReady, StateReclaiming, StateDeleted)
	s.assertAttemptRefused(sess.ID, ErrStateConflict)
}

func (s *StoreSuite) TestCountReclaimAttemptRefusesToExceedTheCap() {
	sess := validSession()
	sess.State, sess.Attempts = StateReclaiming, maxReclaimAttempts
	s.writeSession(sess)
	s.assertAttemptRefused(sess.ID, errInvalidSession)
}

func (s *StoreSuite) TestCountReclaimAttemptReturnsZeroOnRenameFailure() {
	sess := s.create()
	s.move(sess.ID, StateQueued, StateReclaiming)
	s.stubRename(func(*os.Root, string, string) error { return errInjectedIO })
	s.assertAttemptRefused(sess.ID, errInjectedIO)
}

func (s *StoreSuite) assertAttemptRefused(id string, want error) {
	before := s.snapshot()
	got, err := s.store.CountReclaimAttempt(id)
	s.Require().ErrorIs(err, want)
	s.Zero(got)
	s.Equal(before, s.snapshot())
}
