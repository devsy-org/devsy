package gpg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupGpgConf_WritesRequiredDirectives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	g := &GPGConf{}
	require.NoError(t, g.SetupGpgConf())

	got := readConf(t, g.getConfigPath())
	for _, d := range gpgConfDirectives {
		assert.Contains(t, got, d, "gpg.conf must enable %q for forwarding", d)
	}
	assert.Contains(t, gpgConfDirectives, "no-autostart",
		"no-autostart is required to stop gpg spawning a local agent that clobbers the forwarded socket")
}

func TestSetupGpgConf_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	g := &GPGConf{}
	require.NoError(t, g.SetupGpgConf())
	require.NoError(t, g.SetupGpgConf())

	for _, d := range gpgConfDirectives {
		assert.Equal(t, 1, strings.Count(readConf(t, g.getConfigPath()), d+"\n"),
			"directive %q should not be duplicated on repeated setup", d)
	}
}

func TestSetupGpgConf_PreservesExistingDirectives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	g := &GPGConf{}
	require.NoError(t, os.WriteFile(g.getConfigPath(), []byte("use-agent\n"), 0o600))
	require.NoError(t, g.SetupGpgConf())

	got := readConf(t, g.getConfigPath())
	assert.Equal(t, 1, strings.Count(got, "use-agent\n"))
	assert.Contains(t, got, "no-autostart")
}

func TestSetupGpgConf_ExistingFileWithoutTrailingNewline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	g := &GPGConf{}
	// no trailing newline on the existing directive
	require.NoError(t, os.WriteFile(g.getConfigPath(), []byte("use-agent"), 0o600))
	require.NoError(t, g.SetupGpgConf())

	got := readConf(t, g.getConfigPath())
	assert.NotContains(t, got, "use-agentno-autostart", "directives must not be concatenated")
	for _, line := range []string{"use-agent", "no-autostart"} {
		assert.True(t, containsDirective(got, line), "expected %q on its own line, got:\n%s", line, got)
	}
}

func readConf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}
