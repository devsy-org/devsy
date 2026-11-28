//go:build !windows

package events

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/require"
)

const policyVersion = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

func openStore(t *testing.T) (*audit.Store, *audit.Writer, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "audit")
	store, err := audit.Open(dir)
	require.NoError(t, err)
	writer, err := audit.NewWriter(store, audit.WriterConfig{
		Pool:          "test-pool",
		Host:          "h_test",
		PolicyVersion: policyVersion,
		ConfigVersion: policyVersion,
	})
	require.NoError(t, err)
	return store, writer, dir
}

func scanAll(t *testing.T, store *audit.Store) []string {
	t.Helper()
	var lines []string
	require.NoError(t, store.Scan("", "", func(_ string, record []byte) error {
		lines = append(lines, string(record))
		return nil
	}))
	return lines
}

func newTestRecorder(t *testing.T, sink Sink, show func(string) bool) *Recorder {
	t.Helper()
	rec, err := New(Config{
		Sink:        sink,
		Session:     testSession,
		Actor:       envelope.Actor{Kind: envelope.ActorAgent, ID: agentName},
		ShowAddress: show,
	})
	require.NoError(t, err)
	return rec
}

// A shown address of each kind must pass the real writer's checks.
func TestShownAddressesPassTheAuditWriter(t *testing.T) {
	store, writer, _ := openStore(t)
	rec := newTestRecorder(t, writer, showAll)

	for _, host := range []string{"a.example.com", "203.0.113.7", peerV6} {
		rec.Denied(denial(host, 443, egress.NotAllowed))
		rec.Allowed(egress.Allow{Target: egress.Target{Host: host, Port: 443}})
	}
	require.NoError(t, rec.Flush())
	require.Zero(t, rec.Stats().Failures)
	require.Len(t, scanAll(t, store), 6)
}

// The events must pass the real writer's checks, and a token-like host must
// not reach the file while no address filter is set.
func TestEventsPassTheAuditWriter(t *testing.T) {
	store, writer, dir := openStore(t)
	rec := newTestRecorder(t, writer, nil)

	const canary = "tok-canary-secret.example.com"
	for _, reason := range []egress.Reason{
		egress.NotAllowed, egress.ReservedRange, egress.PrivateRange, egress.PoolNetwork,
		egress.IPLiteral, egress.ResolveFailed, egress.BadRequest, egress.SNIMismatch, egress.SNIMissing,
	} {
		rec.Denied(denial(canary, 443, reason))
	}
	rec.Allowed(egress.Allow{Target: egress.Target{Host: canary, Port: 443}})
	require.NoError(t, rec.Flush())
	require.Zero(t, rec.Stats().Failures)

	lines := scanAll(t, store)
	require.Len(t, lines, 10)
	for _, line := range lines {
		require.NotContains(t, line, "canary")
	}
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	require.True(t, strings.Contains(lines[len(lines)-1], "egress.allow.summary"))
}

// A peer address and a rule index must pass the real writer's checks.
func TestPeerAndRuleIndexPassTheAuditWriter(t *testing.T) {
	store, writer, _ := openStore(t)
	rec := newTestRecorder(t, writer, nil)

	for _, peer := range []string{peerV4, peerV6, "::ffff:10.0.0.1", "fe80::1%eth0"} {
		d := denial("a.example.com", 443, egress.PoolNetwork)
		d.Peer = netip.MustParseAddr(peer)
		d.Port += len(peer)
		rec.Denied(d)
	}
	rec.Allowed(egress.Allow{
		Target: egress.Target{Host: "a.example.com", Port: 443},
		Rule:   egress.RuleAt(7),
	})
	require.NoError(t, rec.Flush())
	require.Zero(t, rec.Stats().Failures)

	lines := scanAll(t, store)
	require.Len(t, lines, 5)
	require.Contains(t, lines[0], peerV4)
	require.Contains(t, lines[4], "devsy.egress.rule_index")
}
