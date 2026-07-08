package agent

import (
	"testing"
)

const explicitAgentDir = "/some/dir"

// withContainerDetector swaps the package-level container detector for
// the duration of the test, restoring the previous value on cleanup.
func withContainerDetector(t *testing.T, fn func() bool) {
	t.Helper()
	prev := containerDetector
	containerDetector = fn
	t.Cleanup(func() {
		containerDetector = prev
	})
}

type hostInvocationCase struct {
	name          string
	agentFolder   string
	containerSeen bool
	want          bool
}

func hostInvocationCases() []hostInvocationCase {
	return []hostInvocationCase{
		{
			name:          "host: no agentFolder, not in a container",
			agentFolder:   "",
			containerSeen: false,
			want:          true,
		},
		{
			name:          "container: no agentFolder, running in a container",
			agentFolder:   "",
			containerSeen: true,
			want:          false,
		},
		{
			name:          "explicit agentFolder, not in a container (legacy/explicit)",
			agentFolder:   explicitAgentDir,
			containerSeen: false,
			want:          false,
		},
		{
			name:          "explicit agentFolder beats container detection",
			agentFolder:   explicitAgentDir,
			containerSeen: true,
			want:          false,
		},
	}
}

// TestIsHostAgentInvocation covers the matrix of
// (agentFolder empty/non-empty) x (in a container or not).
func TestIsHostAgentInvocation(t *testing.T) {
	for _, tc := range hostInvocationCases() {
		t.Run(tc.name, func(t *testing.T) {
			withContainerDetector(t, func() bool { return tc.containerSeen })

			got := IsHostAgentInvocation(tc.agentFolder)
			if got != tc.want {
				t.Fatalf(
					"IsHostAgentInvocation(%q) with container=%v = %v, want %v",
					tc.agentFolder, tc.containerSeen, got, tc.want,
				)
			}
		})
	}
}

// TestIsHostAgentInvocation_IgnoresDevsyHome guards the regression that
// setting DEVSY_HOME on the host must NOT flip the predicate to the
// container side — only actually running in a container does that.
func TestIsHostAgentInvocation_IgnoresDevsyHome(t *testing.T) {
	t.Setenv("DEVSY_HOME", "/custom/devsy/home")
	withContainerDetector(t, func() bool { return false })

	if !IsHostAgentInvocation("") {
		t.Fatal("IsHostAgentInvocation should still report host when only DEVSY_HOME is set")
	}
}
