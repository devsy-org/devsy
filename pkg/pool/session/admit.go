package session

import (
	"errors"
	"math"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

// ErrPoolFull means admission can be retried after capacity is freed.
var ErrPoolFull = errors.New("pool full")

var (
	errInvalidLimits = errors.New("pool limits require MaxConcurrent >= 1 and MaxQueued >= 0")
	errSeqExhausted  = errors.New("session admission sequence exhausted")
)

// Limits bounds active sessions and total admissions. It comes from config
// when opening a Store.
type Limits struct {
	MaxConcurrent int
	MaxQueued     int
}

// LimitsFrom takes the store limits from the pool capacity config.
func LimitsFrom(c poolconfig.Capacity) Limits {
	return Limits{MaxConcurrent: c.MaxConcurrent, MaxQueued: c.MaxQueued}
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
func (s *Store) Admit(key string) (Admission, error) {
	unlock, err := s.hold()
	if err != nil {
		return Admission{}, err
	}
	defer unlock()
	hash, err := hashKey(key)
	if err != nil {
		return Admission{}, err
	}
	now := s.now().UTC()
	record, err := s.loadKey(hash)
	if errors.Is(err, ErrNotFound) {
		return s.reserve(keyRecord{Hash: hash}, now)
	}
	if err != nil {
		return Admission{}, err
	}
	return s.retryAdmission(record, now)
}

func (s *Store) retryAdmission(record keyRecord, now time.Time) (Admission, error) {
	sess, expired, err := s.keySession(record, now)
	var seq uint64
	switch {
	case err != nil:
		return Admission{}, err
	case expired:
		return s.reserve(record, now)
	case sess.ID == "":
		seq, err = s.admissionSeq()
		if err != nil {
			return Admission{}, err
		}
	}
	// A failed directory sync can leave a visible, non-durable entry.
	if err := syncRoot(s.keys); err != nil {
		return Admission{}, err
	}
	if sess.ID == "" {
		sess, err = s.createQueued(record.Session, now, seq)
	} else {
		err = syncRoot(s.root)
	}
	if err != nil {
		return Admission{}, err
	}
	return Admission{Session: sess, Existing: true}, nil
}

func (s *Store) reserve(previous keyRecord, now time.Time) (Admission, error) {
	seq, err := s.admissionSeq()
	if err != nil {
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
	sess, err := s.createQueued(id, now, seq)
	return Admission{Session: sess}, err
}

func (s *Store) admissionSeq() (uint64, error) {
	stats, err := s.capacity()
	if err != nil {
		return 0, err
	}
	if uint(stats.active+stats.queued) >= uint(s.limits.MaxConcurrent)+uint(s.limits.MaxQueued) {
		return 0, ErrPoolFull
	}
	return stats.nextSeq()
}

// Promote starts the first queued session in admission order if an active slot is free.
func (s *Store) Promote() (Session, bool, error) {
	unlock, err := s.hold()
	if err != nil {
		return Session{}, false, err
	}
	defer unlock()
	stats, err := s.capacity()
	if err != nil {
		return Session{}, false, err
	}
	if stats.active >= s.limits.MaxConcurrent || stats.queued == 0 {
		return Session{}, false, nil
	}
	next, err := s.transition(stats.oldest.ID, StateQueued, StateSpawning, "")
	return next, err == nil, err
}

type capacity struct {
	active, queued int
	oldest         Session
	maxSeq         uint64
}

func (c capacity) nextSeq() (uint64, error) {
	if c.maxSeq == math.MaxUint64 {
		return 0, errSeqExhausted
	}
	return c.maxSeq + 1, nil
}

func (s *Store) capacity() (capacity, error) {
	list, err := s.list()
	if err != nil {
		return capacity{}, err
	}
	var stats capacity
	for _, sess := range list {
		stats.add(sess)
	}
	return stats, nil
}

func (c *capacity) add(sess Session) {
	c.maxSeq = max(c.maxSeq, sess.Seq)
	switch sess.State {
	case StateDeleted:
	case StateQueued:
		c.queued++
		if c.queued == 1 || queuedBefore(sess, c.oldest) {
			c.oldest = sess
		}
	default:
		// Legacy records without Started take no slot; no production data predates it.
		if sess.Started {
			c.active++
		}
	}
}

func queuedBefore(a, b Session) bool {
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	if cmp := a.Created.Compare(b.Created); cmp != 0 {
		return cmp < 0
	}
	return a.ID < b.ID
}
