package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/stretchr/testify/suite"
)

const (
	auditTestDate = "2026-11-23"
	auditTestTime = auditTestDate + "T00:00:00.000Z"
	auditTestHash = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	auditTestNext = "2026-11-24"
	auditTestTail = `{"torn":`
)

type AuditCmdSuite struct {
	suite.Suite
	dir string
}

func TestAuditCmdSuite(t *testing.T) {
	suite.Run(t, new(AuditCmdSuite))
}

func (s *AuditCmdSuite) SetupTest() {
	s.T().Setenv("DEVSY_UI", "")
	s.dir = s.T().TempDir()
}

func (s *AuditCmdSuite) TestDirectoryFlagRequired() {
	got, err := s.execute()
	s.Require().ErrorContains(err, `required flag(s) "dir" not set`)
	s.Empty(got)
	got, err = s.execute("--dir=")
	s.Require().ErrorContains(err, "--dir must not be empty")
	s.Empty(got)
}

func (s *AuditCmdSuite) TestUnsupportedOutput() {
	for _, mode := range []string{"plain", "auto", ""} {
		got, err := s.execute("--dir", s.dir, "--output", mode)
		s.Require().ErrorContains(err, "only supports --output json")
		s.Empty(got)
	}
}

func (s *AuditCmdSuite) TestDesktopRequiresJSONOutput() {
	s.T().Setenv("DEVSY_UI", "true")
	got, err := s.execute("--dir", s.dir, "--output", "plain")
	s.Require().ErrorContains(err, "only supports --output json")
	s.Empty(got)

	s.skipWindows()
	record := s.record("two", auditTestTime) + "\n"
	s.writeDay(auditTestDate, record)
	got, err = s.execute("--dir", s.dir, "--output", "json")
	s.Require().NoError(err)
	s.Equal(record, got)
}

func (s *AuditCmdSuite) TestWriteAuditRecordDoesNotChangeBackingBuffer() {
	const original = "{ \"n\": 1 }!"
	backing := []byte(original)
	record := backing[:len(backing)-1]
	var out bytes.Buffer
	s.Require().NoError(writeAuditRecord(&out, record))
	s.Equal(original, string(backing))
	s.Equal(original[:len(original)-1]+"\n", out.String())
}

func (s *AuditCmdSuite) TestInvalidSince() {
	for _, value := range []string{"", "yesterday", "2026-11-23T00:00:00", "2026-02-30"} {
		got, err := s.execute("--dir", s.dir, "--since", value)
		s.Require().ErrorIs(err, errAuditSince)
		s.Empty(got)
	}
}

func (s *AuditCmdSuite) TestOutputMatchesStoredBytes() {
	s.skipWindows()
	st, err := audit.Open(s.dir)
	s.Require().NoError(err)
	first := s.record("one", auditTestTime)
	second := s.record("two", auditTestTime)
	s.Require().NoError(st.Append([]byte(first)))
	s.Require().NoError(st.Append([]byte(second)))
	got, err := s.execute("--dir", s.dir)
	s.Require().NoError(err)
	s.Equal(first+"\n"+second+"\n", got)
	got, err = s.execute("--dir", s.dir, "--output", "json", "--session", "two")
	s.Require().NoError(err)
	s.Equal(second+"\n", got)
}

func (s *AuditCmdSuite) TestSinceDateAndNonUTCOffset() {
	s.skipWindows()
	early := s.record("one", "2026-11-22T23:59:59.000Z")
	keep := s.record("two", auditTestTime)
	stored := early + "\n" + keep + "\n"
	s.Require().
		NoError(os.WriteFile(filepath.Join(s.dir, auditTestDate+".jsonl"), []byte(stored), 0o600))
	for _, value := range []string{auditTestDate, "2026-11-23T01:00:00+01:00", "2026-11-22T19:00:00-05:00"} {
		got, err := s.execute("--dir", s.dir, "--since", value)
		s.Require().NoError(err)
		s.Equal(keep+"\n", got)
		t, err := parseAuditSince(value)
		s.Require().NoError(err)
		s.Equal(time.UTC, t.Location())
	}
}

func (s *AuditCmdSuite) TestSinceComparesSubMillisecondCutoffs() {
	s.skipWindows()
	record := s.record("one", auditTestDate+"T00:00:00.123Z")
	s.Require().
		NoError(os.WriteFile(filepath.Join(s.dir, auditTestDate+".jsonl"), []byte(record+"\n"), 0o600))
	got, err := s.execute("--dir", s.dir, "--since", auditTestDate+"T00:00:00.123Z")
	s.Require().NoError(err)
	s.Equal(record+"\n", got)
	got, err = s.execute("--dir", s.dir, "--since", auditTestDate+"T00:00:00.123000001Z")
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *AuditCmdSuite) TestErrorKeepsEarlierOutput() {
	s.skipWindows()
	good := s.record("one", auditTestTime) + "\n"
	name := auditTestDate + ".jsonl"
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, name), []byte(good+"[]\n{}\n"), 0o600))
	got, err := s.execute("--dir", s.dir)
	s.Require().ErrorContains(err, name+" at byte offset ")
	s.Equal(good, got)
}

func (s *AuditCmdSuite) TestNewestTornTailIsSilent() {
	s.skipWindows()
	first := s.record("one", auditTestTime) + "\n"
	last := s.record("two", auditTestTime) + "\n"
	s.writeDay(auditTestDate, first)
	s.writeDay(auditTestNext, last+auditTestTail)
	stdout, stderr, err := s.executeWithStderr("--dir", s.dir)
	s.Require().NoError(err)
	s.Equal(first+last, stdout)
	s.Empty(stderr)
}

func (s *AuditCmdSuite) TestOlderTornTailWarnsOnlyOnStderr() {
	s.skipWindows()
	first := s.record("one", auditTestTime) + "\n"
	last := s.record("two", auditTestTime) + "\n"
	path := s.writeDay(auditTestDate, first+auditTestTail)
	s.writeDay(auditTestNext, last+auditTestTail)
	stdout, stderr, err := s.executeWithStderr("--dir", s.dir)
	s.Require().NoError(err)
	s.Equal(first+last, stdout)
	s.Equal(
		fmt.Sprintf("warning: audit file %s at byte offset %d: skipped unterminated audit record\n",
			path, len(first)),
		stderr,
	)
}

func (s *AuditCmdSuite) execute(args ...string) (string, error) {
	out, _, err := s.executeWithStderr(args...)
	return out, err
}

func (s *AuditCmdSuite) executeWithStderr(args ...string) (string, string, error) {
	var out, stderr bytes.Buffer
	cmd := NewPoolCmd(&flags.GlobalFlags{})
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"audit"}, args...))
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func (s *AuditCmdSuite) writeDay(day, content string) string {
	path := filepath.Join(s.dir, day+".jsonl")
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}

func (s *AuditCmdSuite) record(session, ts string) string {
	body, err := json.Marshal(envelope.Envelope{
		V:       envelope.Version,
		Kind:    envelope.KindAudit,
		Type:    envelope.AuditSessionRequest,
		ID:      envelope.NewID(),
		Src:     "pool",
		TS:      ts,
		Session: session,
		Pool:    "test-pool", Host: "test-host",
		PolicyVersion: auditTestHash, ConfigVersion: auditTestHash,
		Actor: &envelope.Actor{Kind: envelope.ActorAgent, ID: "test-agent"},
		Data: map[string]any{
			"devsy.request.id_hash": strings.Repeat("a", 64), "devsy.request.outcome": "new",
			"devsy.agent.name": "claude", "devsy.requester.kind": "local_user",
			"user.id": "operator", "vcs.repository.name": "owner/name",
		},
	})
	s.Require().NoError(err)
	return "{  " + string(body[1:]) + " \t"
}

func (s *AuditCmdSuite) skipWindows() {
	if runtime.GOOS == "windows" {
		s.T().Skip("opening a pool audit directory is not supported on Windows")
	}
}
