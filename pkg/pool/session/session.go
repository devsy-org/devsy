package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

const (
	schemaVersion      = 1
	maxReclaimAttempts = 1_000_000
)

var (
	reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

	errInvalidSession = errors.New("session record is not valid")
)

// Session is one session file. Reason is a short machine token such as
// pool_restart, never free text. Seq is the admission order; zero means the
// session predates sequencing. Started stays true once a session is promoted.
type Session struct {
	V        int       `json:"v"`
	ID       string    `json:"id"`
	Pool     string    `json:"pool"`
	State    State     `json:"state"`
	Reason   string    `json:"reason,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Seq      uint64    `json:"seq,omitempty"`
	Started  bool      `json:"started,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
}

func (s Session) validate() error {
	switch {
	case s.V != schemaVersion,
		!OwnsID(s.Pool, s.ID),
		!s.State.Known(),
		s.startedFlagInconsistent(),
		s.Reason != "" && !reasonPattern.MatchString(s.Reason),
		s.Created.IsZero(),
		s.Updated.IsZero():
		return errInvalidSession
	}
	return s.validateAttempts()
}

// startedFlagInconsistent reports a Started flag that contradicts the state.
// Legacy records (Seq zero) predate the flag and may lack it in started states.
func (s Session) startedFlagInconsistent() bool {
	if s.Started {
		return s.State == StateQueued
	}
	return s.Seq != 0 && s.State.requiresStarted()
}

func (s Session) validateAttempts() error {
	if s.Attempts < 0 || s.Attempts > maxReclaimAttempts {
		return errInvalidSession
	}
	if s.Attempts != 0 && !s.State.canCountReclaimAttempt() && s.State != StateDeleted {
		return errInvalidSession
	}
	return nil
}

func encode(s Session) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// decode accepts only the exact bytes encode writes. That refuses unknown
// fields, trailing data, duplicate keys and keys that differ only in case,
// which the JSON decoder alone would merge or ignore. Errors never carry file
// content.
func decode(data []byte) (Session, error) {
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, errInvalidSession
	}
	canonical, err := encode(s)
	if err != nil || !bytes.Equal(canonical, data) {
		return Session{}, errInvalidSession
	}
	return s, nil
}
