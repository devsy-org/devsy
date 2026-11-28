//go:build !windows

package events

import (
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

// The events must pass the real writer's checks, and a token-like host must
// not reach the file while no address filter is set.
func TestEventsPassTheAuditWriter(t *testing.T) {
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
	rec, err := New(Config{
		Sink:    writer,
		Session: testSession,
		Actor:   envelope.Actor{Kind: envelope.ActorAgent, ID: agentName},
	})
	require.NoError(t, err)

	const canary = "tok-canary-secret.example.com"
	for _, reason := range []egress.Reason{
		egress.NotAllowed, egress.ReservedRange, egress.PrivateRange, egress.PoolNetwork,
		egress.IPLiteral, egress.ResolveFailed, egress.BadRequest, egress.SNIMismatch, egress.SNIMissing,
	} {
		rec.Denied(denial(canary, 443, reason))
	}
	rec.Allowed(egress.Target{Host: canary, Port: 443})
	require.NoError(t, rec.Flush())
	require.Zero(t, rec.Stats().Failures)

	var lines []string
	require.NoError(t, store.Scan("", "", func(_ string, record []byte) error {
		lines = append(lines, string(record))
		return nil
	}))
	require.Len(t, lines, 10)
	for _, line := range lines {
		require.NotContains(t, line, "canary")
	}
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	require.True(t, strings.Contains(lines[len(lines)-1], "egress.allow.summary"))
}
