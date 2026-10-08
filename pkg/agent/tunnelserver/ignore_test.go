package tunnelserver

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceIgnorePresence(t *testing.T) {
	root := t.TempDir()
	policy, err := loadWorkspaceIgnore(root)
	require.NoError(t, err)
	require.False(t, policy.present)
	require.NoError(t, os.WriteFile(filepath.Join(root, pkgconfig.IgnoreFileName), nil, 0o600))
	policy, err = loadWorkspaceIgnore(root)
	require.NoError(t, err)
	require.True(t, policy.present)
	require.Empty(t, policy.patterns)
}

func TestWorkspaceIgnoreErrorsStopAllTransfers(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreadable), func(t *testing.T) {
			root := t.TempDir()
			ignore := filepath.Join(root, pkgconfig.IgnoreFileName)
			if unreadable {
				require.NoError(t, os.Mkdir(ignore, 0o700))
			} else {
				require.NoError(t, os.WriteFile(ignore, []byte("[\n"), 0o600))
			}
			assertWorkspacePolicyStopsTransfers(t, root)
		})
	}
}

func TestWorkspaceMountIdentityAndSetupValidation(t *testing.T) {
	results := []*config.Result{
		nil,
		{},
		{SubstitutionContext: &config.SubstitutionContext{}},
		{MergedConfig: &config.MergedDevContainerConfig{}},
	}
	for _, result := range results {
		_, err := newSetupServer(result)
		require.ErrorContains(t, err, "invalid setup result")
	}
	info := newSetupInfo(t)
	workspace := config.GetWorkspaceMount(info)
	info.MergedConfig.Mounts = append(
		info.MergedConfig.Mounts,
		workspace,
		&config.Mount{Type: testBindMountType, Source: workspace.Source, Target: "/second"},
	)
	server, err := newSetupServer(info)
	require.NoError(t, err)
	require.Len(t, server.mounts, 3)
	require.Nil(t, server.workspace)
	entries := streamMountEntries(
		t,
		server,
		info.MergedConfig.Mounts[len(info.MergedConfig.Mounts)-1],
	)
	require.Contains(t, entries, "web/node_modules/lib/index.js")
	bad := New(
		WithMounts([]*config.Mount{workspace}),
		WithWorkspaceMount(
			&config.Mount{Type: testBindMountType, Source: workspace.Source, Target: "/wrong"},
		),
	)
	require.ErrorContains(t, bad.validateMountRoles(), "authorized mount set")
}

func TestExactGeneratedArtifactProtection(t *testing.T) {
	info := newSetupInfo(t)
	mount := config.GetWorkspaceMount(info)
	generated := ".devcontainer/" + config.DevsyContextFeatureFolder + "/feature/install.sh"
	writeFiles(
		t,
		mount.Source,
		generated,
		"private/"+config.DevsyContextFeatureFolder+"/secret.key",
		".devcontainer/private.txt",
		"run.sh",
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(mount.Source, pkgconfig.IgnoreFileName),
			[]byte(".devcontainer/\n**/"+config.DevsyContextFeatureFolder+"/\n**/*.sh\n"),
			0o600,
		),
	)
	info.GeneratedBuildContext = filepath.Join(mount.Source, ".devcontainer")
	info.GeneratedBuildArtifacts = []config.GeneratedBuildArtifact{
		{
			Path:   filepath.Join(mount.Source, generated),
			SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(generated))),
		},
	}
	server, err := newSetupServer(info)
	require.NoError(t, err)
	entries := streamMountEntries(t, server, mount)
	require.Contains(t, entries, generated)
	require.NotContains(t, entries, ".devcontainer/private.txt")
	require.NotContains(t, entries, "private/"+config.DevsyContextFeatureFolder+"/secret.key")
	require.NotContains(t, entries, "run.sh")
	require.NoError(t, os.WriteFile(info.GeneratedBuildArtifacts[0].Path, []byte("changed"), 0o600))
	stream := &mockStreamMountServer{}
	require.ErrorContains(
		t,
		server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
		"changed since build",
	)
	require.Zero(t, stream.content.Len())
}

func TestSnapshotWorkspacePolicyDoesNotFilterOtherMount(t *testing.T) {
	info := newSetupInfo(t)
	server, err := newSetupServer(info)
	require.NoError(t, err)
	other := info.MergedConfig.Mounts[0]
	writeFiles(t, other.Source, config.DevsyContextFeatureFolder+"/keep.txt")
	stream := &fakeStreamServer{}
	require.NoError(t, server.StreamSnapshotVolumes(&tunnel.Empty{}, stream))
	names := tarEntryNames(t, stream.chunks)
	require.Contains(t, names, "home/user/.other/node_modules/lib/index.js")
	require.Contains(t, names, "home/user/.other/"+config.DevsyContextFeatureFolder+"/keep.txt")
	require.NotContains(t, names, "workspaces/project/web/node_modules/lib/index.js")
	require.Contains(t, names, "workspaces/project/src/main.go")
}

func TestGeneratedArtifactSymlinkFailsBeforeUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges")
	}
	info := newSetupInfo(t)
	mount := config.GetWorkspaceMount(info)
	external := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(external, []byte("external"), 0o600))
	directory := filepath.Join(mount.Source, config.DevsyContextFeatureFolder)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	leaf := filepath.Join(directory, "Dockerfile-without-features")
	require.NoError(t, os.Symlink(external, leaf))
	info.GeneratedBuildContext = mount.Source
	info.GeneratedBuildArtifacts = []config.GeneratedBuildArtifact{
		{Path: leaf, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("external")))},
	}
	server, err := newSetupServer(info)
	require.NoError(t, err)
	stream := &mockStreamMountServer{}
	require.ErrorContains(
		t,
		server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
		"symlink",
	)
	require.Zero(t, stream.content.Len())
}

func TestGeneratedArtifactManifestCannotAuthorizeOtherPaths(t *testing.T) {
	for _, outsideWorkspace := range []bool{false, true} {
		t.Run(fmt.Sprint(outsideWorkspace), func(t *testing.T) {
			info := newSetupInfo(t)
			mount := config.GetWorkspaceMount(info)
			root := mount.Source
			if outsideWorkspace {
				root = t.TempDir()
			}
			artifact := filepath.Join(root, "private.txt")
			require.NoError(t, os.WriteFile(artifact, []byte("private"), 0o600))
			info.GeneratedBuildContext = mount.Source
			info.GeneratedBuildArtifacts = []config.GeneratedBuildArtifact{
				{Path: artifact, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("private")))},
			}
			server, err := newSetupServer(info)
			require.NoError(t, err)
			stream := &mockStreamMountServer{}
			require.ErrorContains(
				t,
				server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
				"outside",
			)
			require.Zero(t, stream.content.Len())
		})
	}
}

func TestDanglingWorkspaceIgnoreStopsAllTransfers(t *testing.T) {
	root := t.TempDir()
	ignore := filepath.Join(root, pkgconfig.IgnoreFileName)
	if err := os.Symlink(filepath.Join(root, "missing-ignore-target"), ignore); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	_, err := loadWorkspaceIgnore(root)
	require.ErrorContains(t, err, "upload stopped")
	require.ErrorContains(t, err, pkgconfig.IgnoreFileName)
	assertWorkspacePolicyStopsTransfers(t, root)
}

func assertWorkspacePolicyStopsTransfers(t *testing.T, root string) {
	t.Helper()
	mount := &config.Mount{Type: testBindMountType, Source: root, Target: "/workspace"}
	server := New(
		WithMounts([]*config.Mount{mount}),
		WithWorkspaceMount(mount),
		WithWorkspace(&provider2.Workspace{Source: provider2.WorkspaceSource{LocalFolder: root}}),
	)
	calls := []func(*mockStreamMountServer) error{
		func(s *mockStreamMountServer) error {
			return server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, s)
		},
		func(s *mockStreamMountServer) error { return server.StreamWorkspace(&tunnel.Empty{}, s) },
		func(s *mockStreamMountServer) error { return server.StreamSnapshotVolumes(&tunnel.Empty{}, s) },
	}
	for _, call := range calls {
		stream := &mockStreamMountServer{}
		err := call(stream)
		require.ErrorContains(t, err, "upload stopped")
		require.ErrorContains(t, err, pkgconfig.IgnoreFileName)
		require.Zero(t, stream.content.Len())
	}
}
