package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/stretchr/testify/require"
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

const (
	workspaceDirTestContext = "dev"
	workspaceDirTestID      = "ws1"
)

type workspaceDirMode struct {
	name string
	host bool
}

var workspaceDirModes = []workspaceDirMode{
	{name: "container", host: false},
	{name: "host", host: true},
}

type workspaceDirCase struct {
	name      string
	context   string
	id        string
	workspace bool  // create the resolved workspace dir
	binaries  bool  // create binaries/ inside it
	wantErr   error // nil: success
}

// workspaceDirRoot prepares a devsy home for one invocation mode. It returns
// the agentDir argument for the public functions and the root holding
// contexts/. Host invocations resolve through DEVSY_HOME and pass "".
func workspaceDirRoot(t *testing.T, host bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	if host {
		withContainerDetector(t, func() bool { return false })
		t.Setenv(config.EnvHome, root)
		return "", root
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "contexts"), 0o700))
	return root, root
}

// wantWorkspaceDir is the layout the public functions must resolve to. Host
// state lives under WorkspaceAgentDir (.../workspaces/<id>/agent); container
// state is the bare .../workspaces/<id> directory.
func wantWorkspaceDir(root string, host bool, context, id string) string {
	dir := filepath.Join(root, "contexts", context, "workspaces", id)
	if host {
		return filepath.Join(dir, "agent")
	}
	return dir
}

func resolvedContext(context string) string {
	if context == "" {
		return config.DefaultContext
	}
	return context
}

func TestGetAgentBinariesDir(t *testing.T) {
	cases := []workspaceDirCase{
		{
			name:      "binaries present",
			context:   workspaceDirTestContext,
			id:        workspaceDirTestID,
			workspace: true,
			binaries:  true,
		},
		{
			name:      "binaries default context",
			id:        workspaceDirTestID,
			workspace: true,
			binaries:  true,
		},
		{
			name:      "binaries dir not checked",
			context:   workspaceDirTestContext,
			id:        workspaceDirTestID,
			workspace: true,
		},
		{
			name:    "missing workspace",
			context: workspaceDirTestContext,
			id:      workspaceDirTestID,
			wantErr: os.ErrNotExist,
		},
	}
	for _, mode := range workspaceDirModes {
		for _, tc := range cases {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				agentDir, root := workspaceDirRoot(t, mode.host)
				dir := wantWorkspaceDir(root, mode.host, resolvedContext(tc.context), tc.id)
				if tc.workspace {
					require.NoError(t, os.MkdirAll(dir, 0o700))
				}
				if tc.binaries {
					require.NoError(t, os.MkdirAll(filepath.Join(dir, "binaries"), 0o700))
				}

				got, err := GetAgentBinariesDir(agentDir, tc.context, tc.id)
				if tc.wantErr != nil {
					require.ErrorIs(t, err, tc.wantErr)
					require.Equal(t, tc.wantErr, err)
					require.Empty(t, got)
					return
				}
				require.NoError(t, err)
				require.Equal(t, filepath.Join(dir, "binaries"), got)
			})
		}
	}
}

func TestGetAgentWorkspaceDir(t *testing.T) {
	cases := []workspaceDirCase{
		{
			name:      "present",
			context:   workspaceDirTestContext,
			id:        workspaceDirTestID,
			workspace: true,
		},
		{
			name:      "workspace default context",
			id:        workspaceDirTestID,
			workspace: true,
		},
		{
			name:    "missing",
			context: workspaceDirTestContext,
			id:      workspaceDirTestID,
			wantErr: os.ErrNotExist,
		},
	}
	for _, mode := range workspaceDirModes {
		for _, tc := range cases {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				agentDir, root := workspaceDirRoot(t, mode.host)
				dir := wantWorkspaceDir(root, mode.host, resolvedContext(tc.context), tc.id)
				if tc.workspace {
					require.NoError(t, os.MkdirAll(dir, 0o700))
				}

				got, err := GetAgentWorkspaceDir(agentDir, tc.context, tc.id)
				if tc.wantErr != nil {
					require.ErrorIs(t, err, tc.wantErr)
					require.Equal(t, tc.wantErr, err)
					require.Empty(t, got)
					return
				}
				require.NoError(t, err)
				require.Equal(t, dir, got)
			})
		}
	}
}

func TestCreateAgentWorkspaceDir(t *testing.T) {
	cases := []workspaceDirCase{
		{
			name:    "creates dir",
			context: workspaceDirTestContext,
			id:      workspaceDirTestID,
		},
		{
			name: "create default context",
			id:   workspaceDirTestID,
		},
	}
	for _, mode := range workspaceDirModes {
		for _, tc := range cases {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				agentDir, root := workspaceDirRoot(t, mode.host)
				want := wantWorkspaceDir(root, mode.host, resolvedContext(tc.context), tc.id)

				got, err := CreateAgentWorkspaceDir(agentDir, tc.context, tc.id)
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.DirExists(t, got)
			})
		}
	}
}

// Container invocations without contexts/ under agentDir: Get* must report no
// devsy home, while Create* prepares the directory as home.
func TestAgentWorkspaceDir_FindVsPrepareHome(t *testing.T) {
	root := t.TempDir()
	withContainerDetector(t, func() bool { return true })

	_, err := GetAgentWorkspaceDir(root, workspaceDirTestContext, workspaceDirTestID)
	require.ErrorIs(t, err, ErrFindAgentHomeDir)
	_, err = GetAgentBinariesDir(root, workspaceDirTestContext, workspaceDirTestID)
	require.ErrorIs(t, err, ErrFindAgentHomeDir)

	got, err := CreateAgentWorkspaceDir(root, workspaceDirTestContext, workspaceDirTestID)
	require.NoError(t, err)
	want := filepath.Join(
		root, "contexts", workspaceDirTestContext, "workspaces", workspaceDirTestID,
	)
	require.Equal(t, want, got)
	require.DirExists(t, got)
}
