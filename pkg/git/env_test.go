package git

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendGitConfig_PreserveInheritedEntries(t *testing.T) {
	environ := []string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=test.first",
		"GIT_CONFIG_VALUE_0=first",
		"GIT_CONFIG_KEY_1=test.second",
		"GIT_CONFIG_VALUE_1=second",
	}
	baseEnv := []string{"GIT_TERMINAL_PROMPT=0"}

	result := AppendGitConfigWithEnviron(baseEnv, environ, "http.https://github.com/.extraHeader", "secret-token")

	// Verify base environment preserved
	assert.Contains(t, result, "GIT_TERMINAL_PROMPT=0")

	// Verify appended config entries
	assert.Contains(t, result, "GIT_CONFIG_COUNT=3")
	assert.Contains(t, result, "GIT_CONFIG_KEY_2=http.https://github.com/.extraHeader")
	assert.Contains(t, result, "GIT_CONFIG_VALUE_2=secret-token")

	// Verify effective merged environment seen by a child process
	merged := append(environ, result...)
	assert.Equal(t, "3", envValue(merged, "GIT_CONFIG_COUNT"))
	assert.Equal(t, "test.first", envValue(merged, "GIT_CONFIG_KEY_0"))
	assert.Equal(t, "first", envValue(merged, "GIT_CONFIG_VALUE_0"))
	assert.Equal(t, "test.second", envValue(merged, "GIT_CONFIG_KEY_1"))
	assert.Equal(t, "second", envValue(merged, "GIT_CONFIG_VALUE_1"))
	assert.Equal(t, "http.https://github.com/.extraHeader", envValue(merged, "GIT_CONFIG_KEY_2"))
	assert.Equal(t, "secret-token", envValue(merged, "GIT_CONFIG_VALUE_2"))
}

func TestAppendGitConfig_NoInheritedEntries(t *testing.T) {
	baseEnv := []string{"GIT_TERMINAL_PROMPT=0"}

	result := AppendGitConfigWithEnviron(baseEnv, nil, "http.https://github.com/.extraHeader", "secret-token")

	assert.Contains(t, result, "GIT_TERMINAL_PROMPT=0")
	assert.Contains(t, result, "GIT_CONFIG_COUNT=1")
	assert.Contains(t, result, "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader")
	assert.Contains(t, result, "GIT_CONFIG_VALUE_0=secret-token")
}

func TestAppendGitConfig_MalformedInheritedCount(t *testing.T) {
	for _, badCount := range []string{"bogus", "-1", "invalid"} {
		t.Run(badCount, func(t *testing.T) {
			environ := []string{
				"GIT_CONFIG_COUNT=" + badCount,
				"GIT_CONFIG_KEY_0=test.first",
				"GIT_CONFIG_VALUE_0=first",
				"GIT_CONFIG_KEY_1=test.second",
				"GIT_CONFIG_VALUE_1=second",
			}
			baseEnv := []string{"GIT_TERMINAL_PROMPT=0"}

			result := AppendGitConfigWithEnviron(baseEnv, environ, "http.https://github.com/.extraHeader", "secret-token")

			// Even with malformed count, existing keys 0 and 1 are detected and key 2 is appended
			assert.Contains(t, result, "GIT_CONFIG_COUNT=3")
			assert.Contains(t, result, "GIT_CONFIG_KEY_2=http.https://github.com/.extraHeader")
			assert.Contains(t, result, "GIT_CONFIG_VALUE_2=secret-token")
		})
	}
}

func TestAppendGitConfig_BaseEnvAlreadyHasCount(t *testing.T) {
	baseEnv := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=test.initial",
		"GIT_CONFIG_VALUE_0=initial",
	}

	result := AppendGitConfigWithEnviron(baseEnv, nil, "http.https://github.com/.extraHeader", "secret-token")

	// Previous GIT_CONFIG_COUNT=1 in baseEnv must be replaced by GIT_CONFIG_COUNT=2
	countMatches := 0
	for _, entry := range result {
		if strings.HasPrefix(entry, "GIT_CONFIG_COUNT=") {
			countMatches++
		}
	}
	assert.Equal(t, 1, countMatches)
	assert.Contains(t, result, "GIT_CONFIG_COUNT=2")
	assert.Contains(t, result, "GIT_CONFIG_KEY_0=test.initial")
	assert.Contains(t, result, "GIT_CONFIG_KEY_1=http.https://github.com/.extraHeader")
	assert.Contains(t, result, "GIT_CONFIG_VALUE_1=secret-token")
}

func TestAppendGitConfig_GitObservesAllEntries(t *testing.T) {
	environ := []string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=test.first",
		"GIT_CONFIG_VALUE_0=first-value",
		"GIT_CONFIG_KEY_1=test.second",
		"GIT_CONFIG_VALUE_1=second-value",
	}
	baseEnv := []string{"GIT_TERMINAL_PROMPT=0"}

	extra := AppendGitConfigWithEnviron(baseEnv, environ, "test.third", "third-value")
	merged := append(environ, extra...)

	// Execute real git command to confirm git observes all three values via its environment
	runGitConfig := func(key string) string {
		cmd := exec.CommandContext(context.Background(), "git", "config", "--get", key) // #nosec G204
		cmd.Env = merged
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "reading config %s: %s", key, string(out))
		return strings.TrimSpace(string(out))
	}

	assert.Equal(t, "first-value", runGitConfig("test.first"))
	assert.Equal(t, "second-value", runGitConfig("test.second"))
	assert.Equal(t, "third-value", runGitConfig("test.third"))
}

func envValue(env []string, key string) string {
	prefix := key + "="
	last := ""
	for _, entry := range env {
		if val, ok := strings.CutPrefix(entry, prefix); ok {
			last = val
		}
	}
	return last
}
