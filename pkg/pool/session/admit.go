package session

import (
	"errors"
	"time"
)

// ErrPoolFull means admission can be retried after capacity is freed.
var ErrPoolFull = errors.New("pool full")

var errInvalidLimits = errors.New("pool limits require MaxConcurrent >= 1 and MaxQueued >= 0")

// Limits bounds active sessions and total admissions.
type Limits struct {
	MaxConcurrent int
	MaxQueued     int
}

func (l Limits) validate() error {
	if l.MaxConcurrent < 1 || l.MaxQueued < 0 {
		return errInvalidLimits
	}
	return nil
}

// Admission is a queued session or the session already associated with a key.
type Admission struct {
	Session  Session
	Existing bool
}

// Admit returns the session for key, or reserves a queued session if there
// is capacity. An unfinished reservation is completed with its original ID.
func (s *Store) Admit(key string, lim Limits) (Admission, error) {
	unlock, err := s.hold()
	if err != nil {
		return Admission{}, err
	}
	defer unlock()
	if err := lim.validate(); err != nil {
		return Admission{}, err
	}
	hash, err := hashKey(key)
	if err != nil {
		return Admission{}, err
	}
	now := s.now().UTC()
	record, err := s.loadKey(hash)
	if errors.Is(err, ErrNotFound) {
		return s.reserve(keyRecord{Hash: hash}, lim, now)
	}
	if err != nil {
		return Admission{}, err
	}
	return s.retryAdmission(record, lim, now)
}

func (s *Store) retryAdmission(record keyRecord, lim Limits, now time.Time) (Admission, error) {
	sess, expired, err := s.keySession(record, now)
	switch {
	case err != nil:
		return Admission{}, err
	case expired:
		return s.reserve(record, lim, now)
	case sess.ID == "":
		if err := s.checkCapacity(lim); err != nil {
			return Admission{}, err
		}
	}
	// A failed directory sync can leave a visible, non-durable entry.
	if err := syncRoot(s.keys); err != nil {
		return Admission{}, err
	}
	if sess.ID == "" {
		sess, err = s.createQueued(record.Session, now)
	} else {
		err = syncRoot(s.root)
	}
	if err != nil {
		return Admission{}, err
	}
	return Admission{Session: sess, Existing: true}, nil
}

func (s *Store) reserve(previous keyRecord, lim Limits, now time.Time) (Admission, error) {
	if err := s.checkCapacity(lim); err != nil {
		return Admission{}, err
	}
	id, err := s.freshID()
	if err != nil {
		return Admission{}, err
	}
	// The key must survive a crash before the session is published.
	record := keyRecord{V: schemaVersion, Hash: previous.Hash, Session: id, Created: now}
	if err := s.writeKey(record, previous.Session != ""); err != nil {
		return Admission{}, err
	}
	sess, err := s.createQueued(id, now)
	return Admission{Session: sess}, err
}

func (s *Store) checkCapacity(lim Limits) error {
	stats, err := s.capacity()
	if err != nil {
		return err
	}
	if uint(stats.active+stats.queued) >= uint(lim.MaxConcurrent)+uint(lim.MaxQueued) {
		return ErrPoolFull
	}
	return nil
}

// Promote starts the oldest queued session if an active slot is free.
func (s *Store) Promote(lim Limits) (Session, bool, error) {
	unlock, err := s.hold()
	if err != nil {
		return Session{}, false, err
	}
	defer unlock()
	if err := lim.validate(); err != nil {
		return Session{}, false, err
	}
	stats, err := s.capacity()
	if err != nil {
		return Session{}, false, err
	}
	if stats.active >= lim.MaxConcurrent || stats.queued == 0 {
		return Session{}, false, nil
	}
	next, err := s.transition(stats.oldest.ID, StateQueued, StateSpawning, "")
	return next, err == nil, err
}

type capacity struct {
	active, queued int
	oldest         Session
}

func (s *Store) capacity() (capacity, error) {
	list, err := s.list()
	if err != nil {
		return capacity{}, err
	}
	var stats capacity
	for _, sess := range list {
		switch sess.State {
		case StateDeleted:
		case StateQueued:
			stats.queued++
			// The list is in ID order, so keeping the first breaks ties.
			if stats.queued == 1 || sess.Created.Before(stats.oldest.Created) {
				stats.oldest = sess
			}
		default:
			stats.active++
		}
	}
	return stats, nil
}
