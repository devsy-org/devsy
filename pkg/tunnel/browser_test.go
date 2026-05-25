package tunnel

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitError runs a shell command that exits with the given code and returns
// the resulting error (which wraps *exec.ExitError).
func exitError(t *testing.T, code int) error {
	t.Helper()
	// #nosec G204 -- test helper with controlled exit code argument
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	require.Error(t, err)
	return err
}

func baseSSHArgs(ctx, ws string) []string {
	return []string{
		"ssh", "--user=root", "--agent-forwarding=false",
		"--start-services=false", "--context", ctx, ws,
	}
}

func TestBuildSSHCommandArgs(t *testing.T) {
	tests := []struct {
		name      string
		context   string
		workspace string
		debug     bool
		extraArgs []string
		expected  []string
	}{
		{
			name: "basic", context: "default", workspace: "my-workspace",
			expected: baseSSHArgs("default", "my-workspace"),
		},
		{
			name: "with debug", context: "default", workspace: "my-workspace",
			debug:    true,
			expected: append(baseSSHArgs("default", "my-workspace"), "--debug"),
		},
		{
			name: "with extra args", context: "prod", workspace: "ws",
			extraArgs: []string{"--stdio", "--log-output=raw"},
			expected:  append(baseSSHArgs("prod", "ws"), "--stdio", "--log-output=raw"),
		},
		{
			name: "with debug and extra args", context: "default", workspace: "my-workspace",
			debug: true, extraArgs: []string{"--stdio"},
			expected: append(baseSSHArgs("default", "my-workspace"), "--debug", "--stdio"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSSHCommandArgs(tt.context, tt.workspace, tt.debug, tt.extraArgs)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestIsTransientBackhaulErr(t *testing.T) {
	transient := exitError(t, exitcode.WorkspaceNotFound)
	otherExit := exitError(t, 1)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil error", err: nil, expected: false},
		{name: "plain error is not an exit error", err: errors.New("boom"), expected: false},
		{name: "exit code WorkspaceNotFound", err: transient, expected: true},
		{
			name:     "wrapped exit code WorkspaceNotFound",
			err:      fmt.Errorf("wrap: %w", transient),
			expected: true,
		},
		{name: "other exit code", err: otherExit, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isTransientBackhaulErr(tt.err))
		})
	}
}
