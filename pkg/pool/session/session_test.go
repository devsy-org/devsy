package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type SessionSuite struct{ suite.Suite }

func TestSessionSuite(t *testing.T) { suite.Run(t, new(SessionSuite)) }

func validSession() Session {
	at := time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)
	return Session{
		V: 1, ID: "p-pool-aaaaaaaaaa", Pool: "pool", State: StateQueued,
		Created: at, Updated: at,
	}
}

func (s *SessionSuite) TestRoundTrip() {
	want := validSession()
	want.Reason = "pool_restart"
	data, err := encode(want)
	s.Require().NoError(err)
	got, err := decode(data)
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *SessionSuite) TestInvalidFieldsAreRefused() {
	for name, mutate := range map[string]func(*Session){
		"version":        func(x *Session) { x.V = 2 },
		"id":             func(x *Session) { x.ID = "../x" },
		"id other pool":  func(x *Session) { x.ID = "p-other-aaaaaaaaaa" },
		"id prefix pool": func(x *Session) { x.Pool = "poo" },
		"pool empty":     func(x *Session) { x.Pool = "" },
		"pool too long": func(x *Session) {
			x.Pool = strings.Repeat("a", 33)
			x.ID = "p-" + x.Pool + "-aaaaaaaaaa"
		},
		"state":         func(x *Session) { x.State = "running" },
		"state empty":   func(x *Session) { x.State = "" },
		"reason spaces": func(x *Session) { x.Reason = "free text here" },
		"reason upper":  func(x *Session) { x.Reason = "Pool_restart" },
		"reason long":   func(x *Session) { x.Reason = "a" + string(make([]byte, 64)) },
		"created":       func(x *Session) { x.Created = time.Time{} },
		"updated":       func(x *Session) { x.Updated = time.Time{} },
	} {
		sess := validSession()
		mutate(&sess)
		_, err := encode(sess)
		s.Require().ErrorIs(err, errInvalidSession, name)
		data, merr := json.Marshal(sess)
		s.Require().NoError(merr, name)
		_, err = decode(data)
		s.Require().ErrorIs(err, errInvalidSession, name)
	}
}

func (s *SessionSuite) TestDecodeRefusesUnknownFieldsAndTrailingData() {
	data, err := encode(validSession())
	s.Require().NoError(err)
	for name, bad := range map[string]string{
		"unknown field": string(data[:len(data)-1]) + `,"extra":1}`,
		"trailing":      string(data) + "{}",
		"two objects":   string(data) + string(data),
		"not json":      "p-pool",
		"empty":         "",
		"array":         "[]",
	} {
		_, err := decode([]byte(bad))
		s.Require().ErrorIs(err, errInvalidSession, name)
	}
}

func (s *SessionSuite) TestDecodeErrorDoesNotEchoContent() {
	_, err := decode([]byte(`{"v":1,"id":"canary-918273","state":"canary-918273"}`))
	s.Require().Error(err)
	s.NotContains(err.Error(), "canary-918273")
}
