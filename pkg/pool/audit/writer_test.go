//go:build !windows

package audit

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	writerTestID       = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	writerTestCanary   = "private-test-canary"
	writerTestVersion  = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	writerTestTime     = "2026-11-22T10:15:03.123Z"
	writerTestAgent    = "devsy.agent.name"
	writerTestRefKey   = "vcs.ref.head.name"
	writerTestRouteKey = "devsy.route.name"
	writerTestHost     = "example.com"
	writerTestGrant    = "devsy.credential.grant"
	writerTestAddrKey  = "server.address"
	writerTestPortKey  = "server.port"
	writerTestGrantID  = "grant"
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
		PolicyVersion: writerTestVersion,
		ConfigVersion: writerTestVersion,
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
		data := mustJSONForTest(s.T(), event.Data)
		event.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		event.OperationID = "00f067aa0ba902b7"
		event.Workspace = "test-workspace"
		var session string
		if typ != envelope.AuditPolicyApply && typ != envelope.AuditPoolStart {
			session = `,"session":"p-test-pool-k3x7m2q4ab"`
		} else {
			event.Session = ""
		}
		s.Require().NoError(w.Write(event), typ)
		want = append(want, goldenAuditLine(typ, session, data))
	}
	s.Equal(want, s.lines())
}

func (s *WriterSuite) TestWrittenEnvelopeRoundTripsThroughScan() {
	event := writerTestEvent(envelope.AuditEgressAllow)
	s.Require().True(envelope.AuditTypeLocalOnly(event.Type))
	s.Require().NoError(s.writer().Write(event))

	lines := s.lines()
	s.Require().Len(lines, 1)
	got, err := envelope.DecodeStrict([]byte(lines[0]))
	s.Require().NoError(err)
	s.Require().NoError(got.Validate())
	s.JSONEq(mustJSONForTest(s.T(), event.Data), mustJSONForTest(s.T(), got.Data))
	s.Equal(envelope.Envelope{
		V:             envelope.Version,
		Kind:          envelope.KindAudit,
		Type:          event.Type,
		Src:           "pool",
		ID:            writerTestID,
		TS:            writerTestTime,
		Pool:          s.cfg.Pool,
		Host:          s.cfg.Host,
		PolicyVersion: s.cfg.PolicyVersion,
		ConfigVersion: s.cfg.ConfigVersion,
		Session:       event.Session,
		Actor:         &event.Actor,
		Data:          got.Data,
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

func (s *WriterSuite) TestRefusesASessionOfAnotherPool() {
	w := s.writer()
	for _, id := range []string{
		legacySession, "p-other-pool-" + sessionSuffix, "p-test-pool-b-" + sessionSuffix,
	} {
		event := writerTestEvent(envelope.AuditSessionRequest)
		event.Session = id
		err := w.Write(event)
		s.Require().ErrorContains(err, "session is not a session ID of this pool", id)
		s.NotContains(err.Error(), id)
	}
	s.Empty(s.lines())
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
	for name, mutate := range map[string]func(map[string]any){
		"unlisted key":   func(d map[string]any) { d["not.allowed"] = writerTestCanary },
		"bad enum value": func(d map[string]any) { d["devsy.request.outcome"] = writerTestCanary },
		"missing key":    func(d map[string]any) { delete(d, writerTestAgent) },
		"empty key":      func(d map[string]any) { d[""] = writerTestCanary },
		"canary key":     func(d map[string]any) { d[writerTestCanary] = "x" },
	} {
		event := writerTestEvent(envelope.AuditSessionRequest)
		mutate(event.Data)
		err := s.writer().Write(event)
		s.Require().ErrorContains(err, "validation failed", name)
		s.NotContains(err.Error(), writerTestCanary, name)
		s.NoError(errors.Unwrap(err), name)
	}
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesEmptyDataForEveryType() {
	w := s.writer()
	for _, typ := range envelope.AuditTypes() {
		event := writerTestEvent(typ)
		event.Data = map[string]any{}
		s.ErrorContains(w.Write(event), "validation failed", typ)
	}
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesCustomMarshalerWithoutLeakingValues() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[writerTestAgent] = failingAuditValueForTest(writerTestCanary)
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, "validation failed")
	s.NotContains(err.Error(), writerTestCanary)
	s.NoError(errors.Unwrap(err))
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesNilData() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data = nil
	s.ErrorContains(s.writer().Write(event), "validation failed")
	s.Empty(s.lines())
}

func (s *WriterSuite) TestStoresHTMLCharactersUnescaped() {
	const text = "<a href=\"x\">&</a> \\u003c"
	event := writerTestEvent(envelope.AuditCredentialUse)
	event.Data[writerTestRouteKey] = text
	s.Require().NoError(s.writer().Write(event))
	lines := s.lines()
	s.Require().Len(lines, 1)
	s.Contains(lines[0], `"devsy.route.name":"<a href=\"x\">&</a> \\u003c"`)
	s.NotContains(lines[0], `\u003c<`)
	s.NotContains(lines[0], "\\u0026")
	got, err := envelope.DecodeStrict([]byte(lines[0]))
	s.Require().NoError(err)
	s.Equal(text, got.Data[writerTestRouteKey])
}

func (s *WriterSuite) TestRefusesAValueOutsideItsCharacterSet() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[writerTestAgent] = writerTestCanary + " Bearer x"
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, writerTestAgent)
	s.NotContains(err.Error(), writerTestCanary)
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesAnEmptyActorID() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Actor.ID = ""
	err := s.writer().Write(event)
	s.ErrorContains(err, "actor id is empty")
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesAnEmptyID() {
	s.cfg.NewID = func() string { return "" }
	err := s.writer().Write(writerTestEvent(envelope.AuditSessionRequest))
	s.ErrorContains(err, "id is empty")
	s.Empty(s.lines())
}

func (s *WriterSuite) TestRefusesIncompleteConfig() {
	for field, clearField := range map[string]func(*WriterConfig){
		"pool":          func(c *WriterConfig) { c.Pool = "" },
		"host":          func(c *WriterConfig) { c.Host = "" },
		"policyVersion": func(c *WriterConfig) { c.PolicyVersion = "" },
		"configVersion": func(c *WriterConfig) { c.ConfigVersion = "" },
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

func (s *WriterSuite) TestRefusesOversizedEnvelopeWithoutLeakingValues() {
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[writerTestRefKey] = writerTestCanary + strings.Repeat("x", MaxLineBytes)
	err := s.writer().Write(event)
	s.Require().ErrorContains(err, "validation failed")
	s.NotContains(err.Error(), writerTestCanary)
	s.Empty(s.lines())
}

func (s *WriterSuite) TestStoreSyncFailureIsAnError() {
	w := s.writer()
	failSyncOnce(s.T())
	err := w.Write(writerTestEvent(envelope.AuditSessionRequest))
	s.Require().ErrorContains(err, "append audit event")
	s.NotContains(err.Error(), "injected sync failure")
}

func (s *WriterSuite) TestStoreFailureDoesNotExposeThePath() {
	w := s.writer()
	dir := s.store.dir
	s.Require().NoError(os.RemoveAll(dir))
	err := w.Write(writerTestEvent(envelope.AuditSessionRequest))
	s.Require().ErrorIs(err, syscall.ENOENT)
	s.NotContains(err.Error(), dir)
	s.Equal(syscall.ENOENT, errors.Unwrap(err))
}

func (s *WriterSuite) TestConcurrentWritesDoNotInterleave() {
	const writers, perWriter = 8, 10
	s.cfg.NewID = nil
	w := s.writer()
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Data[writerTestRefKey] = strings.Repeat("p", 200)
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
		got, err := envelope.DecodeStrict([]byte(line))
		s.Require().NoError(err)
		s.Require().NoError(got.Validate())
		s.Equal(event.Data[writerTestRefKey], got.Data[writerTestRefKey])
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

func goldenAuditLine(typ, session, data string) string {
	return `{"v":1,"kind":"audit","type":"` + typ + `","id":"` + writerTestID +
		`","ts":"2026-11-22T10:15:03.123Z","src":"pool","traceId":"4bf92f3577b34da6a3ce929d0e0e4736"` +
		`,"operationId":"00f067aa0ba902b7","workspace":"test-workspace"` + session +
		`,"pool":"test-pool","host":"test-host","policyVersion":"` + writerTestVersion +
		`","configVersion":"` + writerTestVersion + `","actor":{"kind":"agent","id":"test-agent"},"data":` + data + `}`
}

func writerTestEvent(typ string) Event {
	return Event{
		Type:    typ,
		Session: "p-test-pool-k3x7m2q4ab",
		Actor:   envelope.Actor{Kind: envelope.ActorAgent, ID: "test-agent"},
		Data:    validAuditDataForTest(typ),
	}
}

func mustJSONForTest(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return string(out)
}

// validAuditDataForTest returns the smallest data the shared allow-list
// accepts for a type. An unregistered type gets no data.
func validAuditDataForTest(typ string) map[string]any {
	for _, group := range []map[string]map[string]any{
		sessionAuditDataForTest(), poolAuditDataForTest(), credentialAuditDataForTest(),
		egressAuditDataForTest(),
	} {
		if data, ok := group[typ]; ok {
			return data
		}
	}
	return map[string]any{}
}

func sessionAuditDataForTest() map[string]map[string]any {
	digest := "sha256:" + strings.Repeat("a", 64)
	return map[string]map[string]any{
		envelope.AuditSessionRequest: {
			"devsy.request.id_hash": strings.Repeat("a", 64), "devsy.request.outcome": "new",
			writerTestAgent: "claude", "devsy.requester.kind": "local_user",
			"user.id": "operator", "vcs.repository.name": "owner/name",
		},
		envelope.AuditSessionSpawn: {"devsy.provider.name": "docker"},
		envelope.AuditSessionReady: {
			"devsy.provider.name": "docker", "container.image.id": digest, "devsy.duration_ms": 10,
		},
		envelope.AuditSessionStart: {
			"process.executable.name": "devsy", "devsy.job.timeout_ms": 1000,
		},
		envelope.AuditSessionIdle: {"devsy.idle_since": writerTestTime},
		envelope.AuditSessionEnd: {
			"devsy.session.end_reason": "completed", "devsy.duration_ms": 10,
		},
		envelope.AuditSessionReclaim: {
			"devsy.reclaim.result": "deleted", "devsy.reclaim.attempts": 1,
			"devsy.reclaim.orphaned": false,
		},
	}
}

func poolAuditDataForTest() map[string]map[string]any {
	return map[string]map[string]any{
		envelope.AuditPolicyApply: {
			"devsy.policy.allow_rule_count": 1, "devsy.policy.grant_names": []any{},
			"devsy.policy.verify_result": "ok",
		},
		envelope.AuditPoolStart: {
			"devsy.pool.sessions_loaded": 1, "devsy.pool.sessions_failed": 0,
			"devsy.pool.orphans_found": 0,
		},
	}
}

func credentialAuditDataForTest() map[string]map[string]any {
	const fingerprint = "0123456789ab"
	return map[string]map[string]any{
		envelope.AuditCredentialIssue: {
			writerTestGrant: writerTestGrantID, "devsy.credential.mode": "inject",
			"devsy.credential.permissions": []any{}, "devsy.credential.repository_count": 1,
			"devsy.credential.ttl_ms": 1000, "devsy.credential.expires_at": writerTestTime,
			"devsy.credential.fingerprint": fingerprint,
		},
		envelope.AuditCredentialUse: {
			writerTestGrant: writerTestGrantID, "devsy.route.name": "route",
			"http.request.method": "GET", "http.request.body.size": 0,
			"http.response.body.size": 0,
		},
		envelope.AuditCredentialDeny: {"devsy.credential.deny_reason": "not_granted"},
		envelope.AuditCredentialRevoke: {
			writerTestGrant: writerTestGrantID, "devsy.credential.fingerprint": fingerprint,
			"devsy.credential.revoke_result": "revoked",
		},
	}
}

func egressAuditDataForTest() map[string]map[string]any {
	dest := func(extra map[string]any) map[string]any {
		data := map[string]any{writerTestAddrKey: writerTestHost, writerTestPortKey: 443}
		maps.Copy(data, extra)
		return data
	}
	return map[string]map[string]any{
		envelope.AuditEgressDeny: dest(map[string]any{"devsy.egress.reason": "not_allowed"}),
		envelope.AuditEgressAllowSummary: dest(map[string]any{
			"devsy.egress.count": 1, "devsy.egress.first_seen": writerTestTime,
			"devsy.egress.last_seen": writerTestTime,
		}),
		envelope.AuditEgressAllow: dest(map[string]any{"network.peer.address": "93.184.216.34"}),
	}
}

type failingAuditValueForTest string

func (v failingAuditValueForTest) MarshalJSON() ([]byte, error) {
	return nil, errors.New(string(v))
}
