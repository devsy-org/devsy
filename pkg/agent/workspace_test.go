package agent

import (
	"testing"
)

// TestIsHostAgentInvocation covers the four quadrants of
// (agentFolder empty/non-empty) x (DEVSY_AGENT_IN_CONTAINER set/unset).
func TestIsHostAgentInvocation(t *testing.T) {
	tests := []struct {
		name        string
		agentFolder string
		inContainer string // empty == unset; "1" == container marker
		want        bool
	}{
		{
			name:        "host: no agentFolder, no marker",
			agentFolder: "",
			inContainer: "",
			want:        true,
		},
		{
			name:        "container: no agentFolder, marker set",
			agentFolder: "",
			inContainer: "1",
			want:        false,
		},
		{
			name:        "explicit agentFolder, no marker (legacy/explicit)",
			agentFolder: "/some/dir",
			inContainer: "",
			want:        false,
		},
		{
			name:        "explicit agentFolder and marker (container with --agent-dir)",
			agentFolder: "/some/dir",
			inContainer: "1",
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Use t.Setenv so the var is reverted after the test, regardless
			// of subtest order. An empty value means "unset".
			if tc.inContainer == "" {
				// Setenv("", "") followed by automatic cleanup is fine; but
				// to truly start from "unset" we set to "" which fails the
				// "== EnvAgentInContainerTrue" check the same way unset does.
				t.Setenv(EnvAgentInContainer, "")
			} else {
				t.Setenv(EnvAgentInContainer, tc.inContainer)
			}

			got := IsHostAgentInvocation(tc.agentFolder)
			if got != tc.want {
				t.Fatalf(
					"IsHostAgentInvocation(%q) with %s=%q = %v, want %v",
					tc.agentFolder, EnvAgentInContainer, tc.inContainer, got, tc.want,
				)
			}
		})
	}
}

// TestIsHostAgentInvocation_IgnoresDevsyHome guards the regression flagged
// in the bug report: setting DEVSY_HOME on the host must NOT flip the
// predicate to "container" — only DEVSY_AGENT_IN_CONTAINER does that.
func TestIsHostAgentInvocation_IgnoresDevsyHome(t *testing.T) {
	t.Setenv("DEVSY_HOME", "/custom/devsy/home")
	t.Setenv(EnvAgentInContainer, "")

	if !IsHostAgentInvocation("") {
		t.Fatal("IsHostAgentInvocation should still report host when only DEVSY_HOME is set")
	}
}

// TestIsHostAgentInvocation_NonStandardMarkerValue ensures that only the
// exact "1" string flips the predicate, mirroring the strict equality
// check in the implementation.
func TestIsHostAgentInvocation_NonStandardMarkerValue(t *testing.T) {
	t.Setenv(EnvAgentInContainer, "true")

	if !IsHostAgentInvocation("") {
		t.Fatal("only DEVSY_AGENT_IN_CONTAINER=1 should be honoured; got false for value 'true'")
	}
}
