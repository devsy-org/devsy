package ssh

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

func TestProxyCommandExcerptProtectsFailureReport(t *testing.T) {
	secret := strings.Repeat("test-value-", 8)
	redactor := secrets.NewEnvironmentRedactor([]string{"DEVSY_TOKEN=" + secret})
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
