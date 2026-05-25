package tunnel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
	tests := []struct {
		name     string
		stderr   string
		expected bool
	}{
		{name: "empty", stderr: "", expected: false},
		{
			name:     "workspace not found exact",
			stderr:   "workspace not found for args: [rust]",
			expected: true,
		},
		{
			name:     "unexpected end of JSON input",
			stderr:   "Error: unexpected end of JSON input\n",
			expected: true,
		},
		{name: "unrelated error", stderr: "some unrelated error", expected: false},
		{
			name:     "embedded in longer blob",
			stderr:   "time=... level=ERROR msg=\"setup failed\" err=\"workspace not found for args: [go]\"\nexit status 1\n",
			expected: true,
		},
		{
			name:     "upper-case variant is case-sensitive",
			stderr:   "Workspace Not Found For Args",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isTransientBackhaulErr(tt.stderr))
		})
	}
}
