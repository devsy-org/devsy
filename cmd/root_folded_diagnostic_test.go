package cmd

import (
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderCLIErrorRedactsFoldedAuthorization(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	original := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = original })
	renderCLIError(&clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "server response Authorization:\n Bearer server-fixture\npublic response",
		Hint:    "try again",
	}, false)
	require.NoError(t, w.Close())
	os.Stderr = original
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "server-fixture")
	assert.Equal(
		t,
		"Error: server response Authorization:***\npublic response\nTry: try again\n",
		string(data),
	)
}
