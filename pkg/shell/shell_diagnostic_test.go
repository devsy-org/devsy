package shell

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserShellMalformedOutputMasksFoldedDiagnostic(t *testing.T) {
	const fixture = "synthetic-getent-continuation"
	shell, err := parseUserShell("user:x:10:10:User:/home/user:/bin/bash")
	require.NoError(t, err)
	assert.Equal(t, "bash", shell)
	_, err = parseUserShell("public account detail\nAuthorization: Bearer\n " + fixture)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public account detail")
	assert.NotContains(t, err.Error(), fixture)
}
