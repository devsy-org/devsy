package poolconfig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidPoolName(t *testing.T) {
	for _, name := range []string{"a", "team-a", "a1", strings.Repeat("a", 32)} {
		require.True(t, ValidPoolName(name), name)
	}
	for _, name := range []string{"", "-a", "a-", "A", "a_b", "a/b", "..", "a b", strings.Repeat("a", 33)} {
		require.False(t, ValidPoolName(name), name)
	}
}
