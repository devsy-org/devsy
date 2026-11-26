package cmdinternal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseExecutablePaths(t *testing.T) {
	paths, err := parseExecutablePaths([]string{"A=/a b/x", "B=/p=q"})

	require.NoError(t, err)
	require.Equal(t, map[string]string{"A": "/a b/x", "B": "/p=q"}, paths)
}

func TestParseExecutablePathsEmpty(t *testing.T) {
	paths, err := parseExecutablePaths(nil)

	require.NoError(t, err)
	require.Nil(t, paths)
}

func TestParseExecutablePathsRejectsMalformedEntries(t *testing.T) {
	for _, entry := range []string{"no-separator", "=/path"} {
		_, err := parseExecutablePaths([]string{entry})

		require.ErrorContains(t, err, "want NAME=PATH", entry)
	}
}

func TestProviderShellExecutablePathFlagIsHidden(t *testing.T) {
	flag := NewProviderShellCmd().Flags().Lookup("executable-path")

	require.NotNil(t, flag)
	require.True(t, flag.Hidden)
}
