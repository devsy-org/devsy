package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

const schemaVersion = 1

var (
	reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

	errInvalidSession = errors.New("session record is not valid")
)

// Session is one session file. Reason is a short machine token such as
// pool_restart, never free text.
type Session struct {
	V       int       `json:"v"`
	ID      string    `json:"id"`
	Pool    string    `json:"pool"`
	State   State     `json:"state"`
	Reason  string    `json:"reason,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

func (s Session) validate() error {
	prefix := idPrefix + s.Pool + "-"
	switch {
	case s.V != schemaVersion,
		!ValidID(s.ID),
		!strings.HasPrefix(s.ID, prefix),
		len(s.ID) != len(prefix)+idSuffixLen,
		!s.State.Known(),
		s.Reason != "" && !reasonPattern.MatchString(s.Reason),
		s.Created.IsZero(),
		s.Updated.IsZero():
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

// decode refuses unknown fields and trailing data. Errors never carry file
// content.
func decode(data []byte) (Session, error) {
	var s Session
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Session{}, errInvalidSession
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Session{}, errInvalidSession
	}
	if err := s.validate(); err != nil {
		return Session{}, err
	}
	return s, nil
}
