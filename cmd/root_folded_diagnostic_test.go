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
	data := captureRenderedCLIError(t, &clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "server response Authorization:\n Bearer server-fixture\npublic response",
		Hint:    "try again",
	})
	assert.Equal(
		t,
		"Error: server response Authorization:***\npublic response\nTry: try again\n",
		data,
	)
}

func TestRenderCLIErrorRedactsMalformedURLCredentials(t *testing.T) {
	data := captureRenderedCLIError(t, &clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "could not connect to https://user:private-url-fixture/part@example.test/",
		Context: map[string]string{
			"response": "https://user:private-url-fixture/ Authorization:@example.test\n Bearer overlap-fixture",
		},
		Hint: "Proxy-Authorization:\nBasic unindented-fixture\nretry later",
	})
	assert.Equal(
		t,
		"Error: could not connect to https://***@example.test/\nContext:\n  response: ***\n"+
			"Try: Proxy-Authorization:***\nretry later\n",
		data,
	)
}

func captureRenderedCLIError(t *testing.T, errToRender *clierr.CLIError) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	original := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = original })
	renderCLIError(errToRender, false)
	require.NoError(t, w.Close())
	os.Stderr = original
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}
