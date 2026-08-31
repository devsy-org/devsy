package git

import (
	"context"
	"errors"
	"testing"

	"gotest.tools/assert"
)

func TestConfigScopeArgs(t *testing.T) {
	testCases := []struct {
		name     string
		scope    ConfigScope
		expected []string
	}{
		{"default", ScopeDefault, nil},
		{"local", ScopeLocal, []string{"--local"}},
		{"global", ScopeGlobal, []string{"--global"}},
		{"system", ScopeSystem, []string{flagSystem}},
		{"file", ScopeFile("/tmp/x.gitconfig"), []string{"--file", "/tmp/x.gitconfig"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.DeepEqual(t, tc.expected, tc.scope.args())
		})
	}
}

func TestConfigGet(t *testing.T) {
	fake := &fakeRunner{stdout: []byte("  ssh\n")}
	config := At("/tmp/repo", WithRunner(fake)).Config()

	val, err := config.Get(context.Background(), "gpg.format", ScopeDefault)
	assert.NilError(t, err)
	assert.Equal(t, "ssh", val) // trimmed
	assert.DeepEqual(t, []string{subConfig, flagGet, "gpg.format"}, fake.lastArgs())
}

func TestConfigGetWithScope(t *testing.T) {
	fake := &fakeRunner{stdout: []byte("user@example.com")}
	config := At("", WithRunner(fake)).Config()

	_, err := config.Get(context.Background(), "user.email", ScopeFile("/home/u/.gitconfig"))
	assert.NilError(t, err)
	assert.DeepEqual(t,
		[]string{subConfig, "--file", "/home/u/.gitconfig", flagGet, "user.email"},
		fake.lastArgs())
}

func TestConfigAddSystemScope(t *testing.T) {
	fake := &fakeRunner{}
	config := At("", WithRunner(fake)).Config()

	err := config.Add(context.Background(), "credential.helper", "!helper", ScopeSystem)
	assert.NilError(t, err)
	assert.DeepEqual(t,
		[]string{subConfig, flagSystem, "--add", "credential.helper", "!helper"},
		fake.lastArgs())
}

func TestConfigSetGlobalScope(t *testing.T) {
	fake := &fakeRunner{}
	config := At("", WithRunner(fake)).Config()

	err := config.Set(context.Background(), "user.signingKey", "KEYID", ScopeGlobal)
	assert.NilError(t, err)
	assert.DeepEqual(t,
		[]string{subConfig, "--global", "user.signingKey", "KEYID"},
		fake.lastArgs())
}

func TestConfigUnsetSystemScope(t *testing.T) {
	fake := &fakeRunner{}
	config := At("", WithRunner(fake)).Config()

	err := config.Unset(context.Background(), "credential.helper", ScopeSystem)
	assert.NilError(t, err)
	assert.DeepEqual(t,
		[]string{subConfig, flagSystem, "--unset", "credential.helper"},
		fake.lastArgs())
}

func TestConfigUnsetValueScopesToExactPattern(t *testing.T) {
	fake := &fakeRunner{}
	config := At("", WithRunner(fake)).Config()

	err := config.UnsetValue(context.Background(), "credential.helper", "!my-helper", ScopeSystem)
	assert.NilError(t, err)
	assert.DeepEqual(t,
		[]string{subConfig, flagSystem, "--unset", "credential.helper", "^!my-helper$"},
		fake.lastArgs())
}

func TestConfigUnsetValueNoMatchIsNotError(t *testing.T) {
	fake := &fakeRunner{err: &CommandError{ExitCode: 5}}
	config := At("", WithRunner(fake)).Config()

	err := config.UnsetValue(context.Background(), "credential.helper", "!my-helper", ScopeSystem)
	assert.NilError(t, err)
}

func TestConfigUnsetValueRealFailurePropagates(t *testing.T) {
	fake := &fakeRunner{err: &CommandError{ExitCode: 128, Stderr: "fatal: bad config"}}
	config := At("", WithRunner(fake)).Config()

	err := config.UnsetValue(context.Background(), "credential.helper", "!my-helper", ScopeSystem)
	assert.Assert(t, err != nil)

	var cmdErr *CommandError
	assert.Assert(t, errors.As(err, &cmdErr))
	assert.Equal(t, 128, cmdErr.ExitCode)
}

func TestConfigGetAbsentKeyIsNotError(t *testing.T) {
	// `git config --get` exits 1 with no output when the key is absent.
	fake := &fakeRunner{err: &CommandError{ExitCode: 1}}
	config := At("/tmp/repo", WithRunner(fake)).Config()

	val, err := config.Get(context.Background(), "user.name", ScopeDefault)
	assert.NilError(t, err)
	assert.Equal(t, "", val)
}

func TestConfigGetRealFailurePropagates(t *testing.T) {
	fake := &fakeRunner{err: &CommandError{ExitCode: 128, Stderr: "fatal: bad config"}}
	config := At("/tmp/repo", WithRunner(fake)).Config()

	_, err := config.Get(context.Background(), "user.name", ScopeDefault)
	assert.Assert(t, err != nil)

	var cmdErr *CommandError
	assert.Assert(t, errors.As(err, &cmdErr))
	assert.Equal(t, 128, cmdErr.ExitCode)
}
