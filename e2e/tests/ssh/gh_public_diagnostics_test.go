package ssh

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicSessionStderrKeepsBoundThroughIOCopy(t *testing.T) {
	input := strings.Repeat("x", 32768)
	var output publicSessionStderr
	n, err := io.Copy(&output, io.LimitReader(strings.NewReader(input), int64(len(input))))
	require.NoError(t, err)
	require.Equal(t, int64(len(input)), n)
	require.Equal(t, input[:8192], output.String())
}
