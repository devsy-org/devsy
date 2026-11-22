package scryptcost

import (
	"bytes"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

func workFactorOf(t *testing.T) string {
	t.Helper()
	recipient, err := Recipient("a long enough passphrase")
	require.NoError(t, err)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	fields := strings.Fields(strings.SplitN(buf.String(), "\n", 3)[1])
	require.Equal(t, "scrypt", fields[1])
	return fields[3]
}

func TestSetOverridesWorkFactorUntilRestored(t *testing.T) {
	restore := Set(8)
	t.Cleanup(restore)
	require.Equal(t, "8", workFactorOf(t))
	restore()
	require.Equal(t, "18", workFactorOf(t))
}
