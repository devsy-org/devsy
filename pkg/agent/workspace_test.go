package agent

import (
	"testing"
)

// explicitAgentDir is a representative non-empty --agent-dir value used to
// exercise the "explicit folder always wins" branch of IsHostAgentInvocation.
const explicitAgentDir = "/some/dir"

// TestAgentCommandEnvPrefix: regressions resurrect the stale-env warning
// on every workspace op against a local provider (docker/podman/k8s).
func TestAgentCommandEnvPrefix(t *testing.T) {
	if got := AgentCommandEnvPrefix(true); got != "" {
		t.Errorf("AgentCommandEnvPrefix(true) = %q, want \"\"", got)
	}
	if got := AgentCommandEnvPrefix(false); got != ContainerAgentEnvPrefix {
		t.Errorf(
			"AgentCommandEnvPrefix(false) = %q, want %q",
			got,
			ContainerAgentEnvPrefix,
		)
	}
}

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
	inContainer   string // empty == unset; "1" == container marker
	containerSeen bool
	want          bool
}

func hostInvocationCases() []hostInvocationCase {
	return []hostInvocationCase{
		{
			name:          "host: no agentFolder, no marker, no indicator",
			agentFolder:   "",
			inContainer:   "",
			containerSeen: false,
			want:          true,
		},
		{
			name:          "container: no agentFolder, marker set, indicator present",
			agentFolder:   "",
			inContainer:   "1",
			containerSeen: true,
			want:          false,
		},
		{
			name:          "host with stale env: marker set but no indicator → host + warn",
			agentFolder:   "",
			inContainer:   "1",
			containerSeen: false,
			want:          true,
		},
		{
			name:          "host with rogue indicator but no env: → host",
			agentFolder:   "",
			inContainer:   "",
			containerSeen: true,
			want:          true,
		},
		{
			name:          "explicit agentFolder, no marker (legacy/explicit)",
			agentFolder:   explicitAgentDir,
			inContainer:   "",
			containerSeen: false,
			want:          false,
		},
		{
			name:          "explicit agentFolder beats stale env",
			agentFolder:   explicitAgentDir,
			inContainer:   "1",
			containerSeen: false,
			want:          false,
		},
		{
			name:          "explicit agentFolder and marker (container with --agent-dir)",
			agentFolder:   explicitAgentDir,
			inContainer:   "1",
			containerSeen: true,
			want:          false,
		},
	}
}

// TestIsHostAgentInvocation covers the matrix of
// (agentFolder empty/non-empty) x (env unset/"1") x (container indicator yes/no).
func TestIsHostAgentInvocation(t *testing.T) {
	for _, tc := range hostInvocationCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvAgentInContainer, tc.inContainer)
			withContainerDetector(t, func() bool { return tc.containerSeen })

			got := IsHostAgentInvocation(tc.agentFolder)
			if got != tc.want {
				t.Fatalf(
					"IsHostAgentInvocation(%q) with %s=%q indicator=%v = %v, want %v",
					tc.agentFolder, EnvAgentInContainer, tc.inContainer,
					tc.containerSeen, got, tc.want,
				)
			}
		})
	}
}

// TestIsHostAgentInvocation_IgnoresDevsyHome guards the regression that
// setting DEVSY_HOME on the host must NOT flip the predicate to
// "container" — only DEVSY_AGENT_IN_CONTAINER + an indicator does that.
func TestIsHostAgentInvocation_IgnoresDevsyHome(t *testing.T) {
	t.Setenv("DEVSY_HOME", "/custom/devsy/home")
	t.Setenv(EnvAgentInContainer, "")
	withContainerDetector(t, func() bool { return false })

	if !IsHostAgentInvocation("") {
		t.Fatal("IsHostAgentInvocation should still report host when only DEVSY_HOME is set")
	}
}

// TestIsHostAgentInvocation_NonStandardMarkerValue ensures that only the
// exact "1" string flips the predicate, mirroring the strict equality
// check in the implementation.
func TestIsHostAgentInvocation_NonStandardMarkerValue(t *testing.T) {
	t.Setenv(EnvAgentInContainer, "true")
	withContainerDetector(t, func() bool { return true })

	if !IsHostAgentInvocation("") {
		t.Fatal("only DEVSY_AGENT_IN_CONTAINER=1 should be honoured; got false for value 'true'")
	}
}
