//go:build !windows

package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/stretchr/testify/suite"
)

const (
	writerTestID     = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	writerTestCanary = "private-test-canary"
)

type WriterSuite struct {
	suite.Suite
	store *Store
	cfg   WriterConfig
}

func TestWriterSuite(t *testing.T) {
	suite.Run(t, new(WriterSuite))
}

func (s *WriterSuite) SetupTest() {
	now := func() time.Time {
		return time.Date(
			2026,
			time.November,
			22,
			2,
			15,
			3,
			123456789,
			time.FixedZone("west", -8*60*60),
		)
	}
	st, err := Open(filepath.Join(s.T().TempDir(), "audit"))
	s.Require().NoError(err)
	st.now = now
	s.store = st
	s.cfg = WriterConfig{
		Pool:          "test-pool",
		Host:          "test-host",
		PolicyVersion: "test-policy",
		ConfigVersion: "test-config",
		Validate:      acceptAuditDataForTest,
		Now:           now,
		NewID:         func() string { return writerTestID },
	}
}

func (s *WriterSuite) TestEveryAuditTypeMatchesGoldenJSON() {
	w := s.writer()
	types := envelope.AuditTypes()
	s.Require().Len(types, 16)
	var want []string
	for _, typ := range types {
		event := writerTestEvent(typ)
		event.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		event.OperationID = "00f067aa0ba902b7"
		event.Workspace = "test-workspace"
		var session string
		if typ != envelope.AuditPolicyApply && typ != envelope.AuditPoolStart {
			session = `,"session":"test-session"`
		} else {
			event.Session = ""
		}
		s.Require().NoError(w.Write(event), typ)
		want = append(want, goldenAuditLine(typ, session))
	}
	s.Equal(want, s.lines())
}

func (s *WriterSuite) TestWrittenEnvelopeRoundTripsThroughScan() {
	event := writerTestEvent(envelope.AuditEgressAllow)
	s.Require().True(envelope.AuditTypeLocalOnly(event.Type))
	s.Require().NoError(s.writer().Write(event))

	lines := s.lines()
	s.Require().Len(lines, 1)
	var got envelope.Envelope
	s.Require().NoError(json.Unmarshal([]byte(lines[0]), &got))
	s.Equal(envelope.Envelope{
		V:             envelope.Version,
		Kind:          envelope.KindAudit,
		Type:          event.Type,
		Src:           "pool",
		ID:            writerTestID,
		TS:            "2026-11-22T10:15:03.123Z",
		Pool:          s.cfg.Pool,
		Host:          s.cfg.Host,
		PolicyVersion: s.cfg.PolicyVersion,
		ConfigVersion: s.cfg.ConfigVersion,
		Session:       event.Session,
		Actor:         &event.Actor,
		Data:          event.Data,
	}, got)
}

func (s *WriterSuite) TestMissingSession() {
	w := s.writer()
	for _, typ := range envelope.AuditTypes() {
		event := writerTestEvent(typ)
		event.Session = ""
		err := w.Write(event)
		if typ == envelope.AuditPolicyApply || typ == envelope.AuditPoolStart {
			s.NoError(err, typ)
		} else {
			s.ErrorContains(err, "session is required", typ)
		}
	}
	s.Len(s.lines(), 2)
}

func (s *WriterSuite) TestRefusesUnregisteredTypeWithoutLeakingValues() {
	event := writerTestEvent(writerTestCanary)
	event.Data[""] = writerTestCanary
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, "unregistered type")
	s.NotContains(err.Error(), writerTestCanary)
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesUnknownActorWithoutLeakingValues() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Actor.Kind = writerTestCanary
	event.Actor.ID = writerTestCanary
	event.Data[""] = writerTestCanary
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, "unknown actor kind")
	s.NotContains(err.Error(), writerTestCanary)
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesInvalidDataWithoutLeakingValues() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[""] = writerTestCanary
	s.cfg.Validate = func(typ string, data map[string]any) error {
		s.Equal(event.Type, typ)
		s.Equal(event.Data, data)
		return fmt.Errorf("refused %v", data[""])
	}
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, "data validation failed")
	s.NotContains(err.Error(), writerTestCanary)
	s.NoError(errors.Unwrap(err))
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesMarshalErrorWithoutLeakingValues() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[""] = failingAuditValueForTest(writerTestCanary)
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, "marshal envelope failed")
	s.NotContains(err.Error(), writerTestCanary)
	s.NoError(errors.Unwrap(err))
	s.Empty(s.lines())
}

func (s *WriterSuite) TestNilDataIsValidatedAndWrittenAsAnEmptyObject() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data = nil
	s.cfg.Validate = func(_ string, data map[string]any) error {
		s.NotNil(data, "the validator must see an empty map, not nil")
		return nil
	}
	s.Require().NoError(s.writer().Write(event))
	lines := s.lines()
	s.Require().Len(lines, 1)
	s.Contains(lines[0], `"data":{}`)
}

func (s *WriterSuite) TestRefusesAnEmptyID() {
	s.cfg.NewID = func() string { return "" }
	err := s.writer().Write(writerTestEvent(envelope.AuditSessionRequest))
	s.ErrorContains(err, "id is empty")
	s.Empty(s.lines())
}

func (s *WriterSuite) TestValidatorSeesEveryRegisteredType() {
	var seen []string
	s.cfg.Validate = func(typ string, _ map[string]any) error {
		seen = append(seen, typ)
		return nil
	}
	w := s.writer()
	for _, typ := range envelope.AuditTypes() {
		s.Require().NoError(w.Write(writerTestEvent(typ)), typ)
	}
	s.Equal(envelope.AuditTypes(), seen)
}

func (s *WriterSuite) TestRefusesIncompleteConfig() {
	for field, clearField := range map[string]func(*WriterConfig){
		"pool":           func(c *WriterConfig) { c.Pool = "" },
		"host":           func(c *WriterConfig) { c.Host = "" },
		"policyVersion":  func(c *WriterConfig) { c.PolicyVersion = "" },
		"configVersion":  func(c *WriterConfig) { c.ConfigVersion = "" },
		"data validator": func(c *WriterConfig) { c.Validate = nil },
	} {
		cfg := s.cfg
		clearField(&cfg)
		w, err := NewWriter(s.store, cfg)
		s.ErrorContains(err, field)
		s.Nil(w)
	}
	w, err := NewWriter(nil, s.cfg)
	s.ErrorContains(err, "requires a store")
	s.Nil(w)
}

func (s *WriterSuite) TestStoreRejectsOversizedEnvelope() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[""] = writerTestCanary + strings.Repeat("x", MaxLineBytes)
	err := s.writer().Write(event)
	s.Require().ErrorIs(err, errLineTooLong)
	s.NotContains(err.Error(), writerTestCanary)
	s.Empty(s.lines())
}

func (s *WriterSuite) TestStoreSyncFailureIsAnError() {
	w := s.writer()
	failSyncOnce(s.T())
	err := w.Write(writerTestEvent(envelope.AuditSessionRequest))
	s.Require().ErrorContains(err, "append audit event")
	s.ErrorContains(err, "injected sync failure")
}

func (s *WriterSuite) TestConcurrentWritesDoNotInterleave() {
	const writers, perWriter = 8, 10
	s.cfg.NewID = nil
	w := s.writer()
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[""] = strings.Repeat("p", 4096)
	results := make(chan error, writers*perWriter)
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range perWriter {
				results <- w.Write(event)
			}
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		s.Require().NoError(err)
	}
	seen := make(map[string]struct{})
	lines := s.lines()
	s.Require().Len(lines, writers*perWriter)
	for _, line := range lines {
		var got envelope.Envelope
		s.Require().NoError(json.Unmarshal([]byte(line), &got))
		s.Equal(event.Data, got.Data)
		s.Len(got.ID, 26)
		seen[got.ID] = struct{}{}
	}
	s.Len(seen, writers*perWriter)
}

func (s *WriterSuite) writer() *Writer {
	w, err := NewWriter(s.store, s.cfg)
	s.Require().NoError(err)
	return w
}

func (s *WriterSuite) lines() []string {
	var got []string
	s.Require().NoError(s.store.Scan("", "", func(day string, rec []byte) error {
		s.Equal(dayOne, day)
		got = append(got, string(rec))
		return nil
	}))
	return got
}

func goldenAuditLine(typ, session string) string {
	return `{"v":1,"kind":"audit","type":"` + typ + `","id":"` + writerTestID +
		`","ts":"2026-11-22T10:15:03.123Z","src":"pool","traceId":"4bf92f3577b34da6a3ce929d0e0e4736"` +
		`,"operationId":"00f067aa0ba902b7","workspace":"test-workspace"` + session +
		`,"pool":"test-pool","host":"test-host","policyVersion":"test-policy","configVersion":"test-config"` +
		`,"actor":{"kind":"agent","id":"test-agent"},"data":{}}`
}

func writerTestEvent(typ string) Event {
	return Event{
		Type:    typ,
		Session: "test-session",
		Actor:   envelope.Actor{Kind: envelope.ActorAgent, ID: "test-agent"},
		Data:    map[string]any{},
	}
}

func acceptAuditDataForTest(string, map[string]any) error { return nil }

type failingAuditValueForTest string

func (v failingAuditValueForTest) MarshalJSON() ([]byte, error) {
	return nil, errors.New(string(v))
}
