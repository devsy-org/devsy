package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gotest.tools/assert"
)

func TestCommandErrorUnwrapAndMessage(t *testing.T) {
	inner := errors.New("exit status 1")
	e := &CommandError{
		Args:     []string{subConfig, flagGet, "missing.key"},
		ExitCode: 1,
		Stderr:   "boom",
		Err:      inner,
	}
	assert.Assert(t, errors.Is(e, inner))
	assert.Assert(t, cmpContains(e.Error(), "config --get missing.key"))
	assert.Assert(t, cmpContains(e.Error(), "boom"))
}

func cmpContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// TestExecRunnerExitCode exercises the real git binary to confirm exit codes
// are captured into CommandError. `git config --get` of an absent key in a
// non-repo directory exits 1.
func TestExecRunnerExitCode(t *testing.T) {
	runner := execRunner{}
	if _, err := runner.Run(
		context.Background(),
		RunOptions{Args: []string{"--version"}},
	); err != nil {
		t.Skipf("git not available: %v", err)
	}

	_, err := runner.Run(context.Background(), RunOptions{
		Dir:  t.TempDir(),
		Args: []string{subConfig, flagGet, "no.such.key.exists"},
	})
	assert.Assert(t, err != nil)

	var cmdErr *CommandError
	assert.Assert(t, errors.As(err, &cmdErr))
	assert.Equal(t, 1, cmdErr.ExitCode)
}
