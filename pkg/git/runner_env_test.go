package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/assert"
)

// TestExecRunnerKeepsFlagOutputWithCredentialNamedEnv pins the redaction of git
// output: a credential-named switch such as NATIVE_CREDENTIAL_TESTS=true is set
// by the macOS CI job and must not turn `true` in git output into a mask.
func TestExecRunnerKeepsFlagOutputWithCredentialNamedEnv(t *testing.T) {
	if _, err := (execRunner{}).Run(
		context.Background(),
		RunOptions{Args: []string{"--version"}},
	); err != nil {
		t.Skipf("git not available: %v", err)
	}
	t.Setenv("NATIVE_CREDENTIAL_TESTS", "true")
	t.Setenv("AUTH_ENABLED", "1")

	file := filepath.Join(t.TempDir(), "gitconfig")
	content := "[credential \"https://github.com\"]\n\tuseHttpPath = true\n\tdepth = 1\n"
	assert.NilError(t, os.WriteFile(file, []byte(content), 0o600))

	config := At("").Config()
	got, err := config.Get(
		context.Background(),
		"credential.https://github.com.useHttpPath",
		ScopeFile(file),
	)
	assert.NilError(t, err)
	assert.Equal(t, "true", got)

	got, err = config.Get(
		context.Background(),
		"credential.https://github.com.depth",
		ScopeFile(file),
	)
	assert.NilError(t, err)
	assert.Equal(t, "1", got)
}
