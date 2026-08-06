package agent

import (
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/util"
)

const explicitAgentDir = "/some/dir"

func TestIsLocalAgent(t *testing.T) {
	cases := []struct {
		name  string
		local types.StrBool
		want  bool
	}{
		{"local true", "true", true},
		{"local false", "false", false},
		{"unset defaults to remote", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isLocalAgent(&provider2.ProviderAgentConfig{Local: tc.local})
			if got != tc.want {
				t.Errorf("isLocalAgent(%q) = %v, want %v", tc.local, got, tc.want)
			}
		})
	}
}

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

func TestCandidateAgentDirs_HonorsDevsyHomeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	dirs := candidateAgentDirs()

	want := filepath.Join(home, "agent")
	found := false
	for _, d := range dirs {
		if d == want {
			found = true
		}
		realHome, _ := util.UserHomeDir()
		if realHome != "" && realHome != home {
			unwanted := filepath.Join(realHome, config.ConfigDirName, "agent")
			if d == unwanted {
				t.Fatalf(
					"candidateAgentDirs() included raw-home candidate %q while DEVSY_HOME=%q was set",
					unwanted,
					home,
				)
			}
		}
	}
	if !found {
		t.Fatalf("candidateAgentDirs() = %v, want it to include %q", dirs, want)
	}
}

func TestIsHostAgentInvocation_IgnoresDevsyHome(t *testing.T) {
	t.Setenv("DEVSY_HOME", "/custom/devsy/home")
	withContainerDetector(t, func() bool { return false })

	if !IsHostAgentInvocation("") {
		t.Fatal("IsHostAgentInvocation should still report host when only DEVSY_HOME is set")
	}
}
