package ssh

import (
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
)

func TestSSHOutputAssertionDoesNotRenderRawCapture(t *testing.T) {
	const fixture = "synthetic-ssh-assertion-value"
	const detail = "public signing detail"
	output := "Authorization:\n Bearer " + fixture + "\n" + detail
	previous := gomega.Default
	gomega.Default = gomega.NewWithT(t)
	t.Cleanup(func() { gomega.Default = previous })
	for _, present := range []bool{true, false} {
		fragment := "missing expected detail"
		if !present {
			fragment = detail
		}
		failures := gomega.InterceptGomegaFailures(func() {
			expectSSHSubstring(output, fragment, "SSH command assertion", present)
		})
		require.Len(t, failures, 1)
		require.Contains(t, failures[0], "SSH command assertion")
		require.Contains(t, failures[0], detail)
		require.NotContains(t, failures[0], fixture)
	}
}

func TestProxyCommandExcerptProtectsFailureReport(t *testing.T) {
	secret := strings.Repeat("test-value-", 8)
	redactor := redact.New(redact.Options{Environment: []string{"DEVSY_TOKEN=" + secret}})
	input := strings.Repeat(
		"padding",
		3000,
	) + "\x1b[31m\n" + secret + " https://user:password@example.com/end"
	got := proxyCommandExcerpt(redactor, input)
	require.LessOrEqual(t, len(got), proxyCommandExcerptLimit+len("[truncated]"))
	require.NotContains(t, got, secret)
	require.NotContains(t, got, "password")
	require.NotContains(t, got, "\x1b")
	require.NotContains(t, got, "\n")
	require.Contains(t, got, `\x1b`)
	require.Contains(t, got, "example.com/end")
	require.Contains(t, got, "***")
}

func TestProxyCommandOutputDiagnosticMasksFoldedCredentials(t *testing.T) {
	const input = "Authorization:\n Bearer proxy-folded-fixture\npublic proxy detail"
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	require.NoError(t, root.WriteFile(proxyCommandTraceFile, []byte(input), 0o600))

	got := proxyCommandOutputDiagnostic(root, "public stdout", input)
	require.Contains(t, got, "public stdout")
	require.Equal(t, 2, strings.Count(got, "public proxy detail"))
	require.NotContains(t, got, "proxy-folded-fixture")
	require.Contains(t, got, "***")
	require.NotContains(t, got, "\n")
}
