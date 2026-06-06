package clientimplementation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnrichCommandError_AppendsCapturedOutput(t *testing.T) {
	base := errors.New("exit status 1")
	out := []byte("Error response from daemon: no such container\n")

	got := enrichCommandError(base, out)
	require.ErrorIs(t, got, base, "wrapped error must preserve the original via errors.Is")
	require.Contains(t, got.Error(), "exit status 1")
	require.Contains(t, got.Error(), "no such container")
}

func TestEnrichCommandError_NoOutput(t *testing.T) {
	base := errors.New("exit status 1")

	require.Equal(t, base, enrichCommandError(base, nil))
	require.Equal(t, base, enrichCommandError(base, []byte("   \n\t")))
}
