package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type SessionSuite struct{ suite.Suite }

var postPromotionStates = [...]State{StateSpawning, StateReady, StateBusy, StateIdle, StateFailed}

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
	want.State = StateReclaiming
	want.Seq, want.Started, want.Attempts = 42, true, maxReclaimAttempts
	want.Reason = "pool_restart"
	data, err := encode(want)
	s.Require().NoError(err)
	got, err := decode(data)
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *SessionSuite) TestLegacyV1RoundTripsByteIdentically() {
	legacy := []byte(`{"v":1,"id":"p-pool-aaaaaaaaaa","pool":"pool","state":"queued",` +
		`"created":"2026-11-25T12:00:00Z","updated":"2026-11-25T12:00:00Z"}`)
	sess, err := decode(legacy)
	s.Require().NoError(err)
	s.Equal(validSession(), sess)
	got, err := encode(sess)
	s.Require().NoError(err)
	s.Equal(legacy, got)
}

func (s *SessionSuite) TestDecodeRefusesSequencedPostPromotionStatesWithoutStarted() {
	for _, state := range postPromotionStates {
		sess := validSession()
		sess.State, sess.Seq = state, 1
		data, err := json.Marshal(sess)
		s.Require().NoError(err)
		got, err := decode(data)
		s.ErrorIs(err, errInvalidSession, "%s", state)
		s.Zero(got, "%s", state)
	}
}

func (s *SessionSuite) TestDecodeAllowsLegacyPostPromotionStatesWithoutStarted() {
	for _, state := range postPromotionStates {
		sess := validSession()
		sess.State = state
		s.assertDecodes(sess)
	}
}

func (s *SessionSuite) TestDecodeAllowsSequencedUnstartedReclaimPath() {
	for _, state := range []State{StateReclaiming, StateOrphaned, StateDeleted} {
		sess := validSession()
		sess.State, sess.Seq = state, 1
		s.assertDecodes(sess)
	}
}

func (s *SessionSuite) TestInvalidAttemptsAreRefused() {
	for _, edit := range []struct {
		state    State
		attempts int
	}{
		{StateReclaiming, -1},
		{StateOrphaned, maxReclaimAttempts + 1},
		{StateReady, 1},
	} {
		sess := validSession()
		sess.State, sess.Attempts = edit.state, edit.attempts
		_, err := encode(sess)
		s.Require().ErrorIs(err, errInvalidSession)
		data, err := json.Marshal(sess)
		s.Require().NoError(err)
		got, err := decode(data)
		s.Require().ErrorIs(err, errInvalidSession)
		s.Zero(got)
	}
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
		"null":          "null",
	} {
		_, err := decode([]byte(bad))
		s.Require().ErrorIs(err, errInvalidSession, name)
	}
}

func (s *SessionSuite) TestDecodeRefusesAnythingButTheCanonicalBytes() {
	canonical, err := encode(validSession())
	s.Require().NoError(err)
	text := string(canonical)
	for name, bad := range map[string]string{
		"duplicate key":     strings.Replace(text, `"state":"queued"`, `"state":"busy","state":"queued"`, 1),
		"duplicate id":      strings.Replace(text, `{"v":1,`, `{"v":1,"id":"p-pool-bbbbbbbbbb",`, 1),
		"case variant key":  strings.Replace(text, `"state":`, `"State":`, 1),
		"case variant dup":  strings.Replace(text, `"v":1,`, `"v":1,"V":2,`, 1),
		"whitespace":        strings.Replace(text, `,`, `, `, 1),
		"trailing newline":  text + "\n",
		"key order":         `{"id":"p-pool-aaaaaaaaaa","v":1,` + text[len(`{"v":1,"id":"p-pool-aaaaaaaaaa",`):],
		"zone offset":       strings.Replace(text, "12:00:00Z", "12:00:00+00:00", 1),
		"escaped character": strings.Replace(text, "queued", "\\u0071ueued", 1),
	} {
		s.Require().NotEqual(text, bad, name)
		_, err := decode([]byte(bad))
		s.Require().ErrorIs(err, errInvalidSession, name)
	}
}

func (s *SessionSuite) TestDecodeRefusesDuplicateAndCaseVariantNewFields() {
	sess := validSession()
	sess.State, sess.Seq, sess.Started, sess.Attempts = StateReclaiming, 1, true, 2
	data, err := encode(sess)
	s.Require().NoError(err)
	for _, field := range []string{`"seq":1`, `"started":true`, `"attempts":2`} {
		for _, replacement := range []string{field + "," + field, strings.ToUpper(field[:2]) + field[2:]} {
			got, err := decode([]byte(strings.Replace(string(data), field, replacement, 1)))
			s.Require().ErrorIs(err, errInvalidSession)
			s.Zero(got)
		}
	}
}

func (s *SessionSuite) TestDecodeErrorDoesNotEchoContent() {
	_, err := decode([]byte(`{"v":1,"id":"canary-918273","state":"canary-918273"}`))
	s.Require().Error(err)
	s.NotContains(err.Error(), "canary-918273")
}

func (s *SessionSuite) assertDecodes(sess Session) {
	data, err := json.Marshal(sess)
	s.Require().NoError(err)
	got, err := decode(data)
	s.Require().NoError(err, "%s", sess.State)
	s.Equal(sess, got)
}
