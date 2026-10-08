package tunnelserver

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	srcMainGo           = "src/main.go"
	testBindMountType   = "bind"
	testNodeModulesFile = "node_modules/lib/index.js"
)

// mockStreamMountServer collects the chunks sent by StreamMount.
type mockStreamMountServer struct {
	grpc.ServerStream

	content bytes.Buffer
}

func (m *mockStreamMountServer) Send(chunk *tunnel.Chunk) error {
	m.content.Write(chunk.Content)
	return nil
}

func (m *mockStreamMountServer) Context() context.Context {
	return context.Background()
}

func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()

	for _, file := range files {
		p := filepath.Join(root, filepath.FromSlash(file))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(file), 0o600))
	}
}

func streamMountEntries(t *testing.T, server *tunnelServer, mount *config.Mount) []string {
	t.Helper()

	stream := &mockStreamMountServer{}
	require.NoError(
		t,
		server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
	)

	entries := []string{}
	reader := tar.NewReader(&stream.content)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries
}

// newSetupInfo returns a setup result with a workspace folder and another bind
// mount, both with an ignore file that excludes node_modules.
func newSetupInfo(t *testing.T) *config.Result {
	t.Helper()

	ignore := "**/node_modules/\n.claude/worktrees/\n"

	workspaceFolder := t.TempDir()
	writeFiles(t, workspaceFolder,
		srcMainGo,
		"web/node_modules/lib/index.js",
		".claude/worktrees/feature/main.go",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(workspaceFolder, pkgconfig.IgnoreFileName), []byte(ignore), 0o600))

	otherFolder := t.TempDir()
	writeFiles(t, otherFolder, "config.json", testNodeModulesFile)
	require.NoError(t, os.WriteFile(
		filepath.Join(otherFolder, pkgconfig.IgnoreFileName), []byte(ignore), 0o600))

	return &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount: "type=bind,src=" + workspaceFolder + ",dst=/workspaces/project",
		},
		MergedConfig: &config.MergedDevContainerConfig{
			NonComposeBase: config.NonComposeBase{
				Mounts: []*config.Mount{
					{Type: testBindMountType, Source: otherFolder, Target: "/home/user/.other"},
					{Type: "volume", Source: "cache", Target: "/cache"},
				},
			},
		},
	}
}

func TestNewSetupServer_MarksWorkspaceMount(t *testing.T) {
	setupInfo := newSetupInfo(t)

	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)

	require.Len(t, server.mounts, 2)
	require.NotNil(t, server.workspaceMount)
	assert.Equal(t, config.GetWorkspaceMount(setupInfo).String(), server.workspaceMount.String())
	assert.Equal(t, server.mounts[0].String(), server.workspaceMount.String())
}

func TestNewSetupServer_WithoutWorkspaceMount(t *testing.T) {
	setupInfo := newSetupInfo(t)
	setupInfo.SubstitutionContext.WorkspaceMount = ""

	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)

	assert.Nil(t, server.workspaceMount)
	require.Len(t, server.mounts, 1)
	entries := streamMountEntries(t, server, setupInfo.MergedConfig.Mounts[0])
	assert.Equal(
		t,
		[]string{pkgconfig.IgnoreFileName, "config.json", testNodeModulesFile},
		entries,
	)
}

func TestStreamMount_WorkspaceMountHonoursIgnoreFile(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.Equal(t, []string{pkgconfig.IgnoreFileName, srcMainGo}, entries)
}

func TestStreamMount_OtherBindMountIsStreamedWhole(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)

	entries := streamMountEntries(t, server, setupInfo.MergedConfig.Mounts[0])

	assert.Equal(
		t,
		[]string{pkgconfig.IgnoreFileName, "config.json", testNodeModulesFile},
		entries,
	)
}

func TestStreamMount_WithoutWorkspaceMountNothingIsExcluded(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := New(WithMounts(config.GetMounts(setupInfo)))

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.Equal(t, []string{
		".claude/worktrees/feature/main.go",
		pkgconfig.IgnoreFileName,
		srcMainGo,
		"web/node_modules/lib/index.js",
	}, entries)
}

func TestStreamMount_InvalidIgnoreFileFailsBeforeUpload(t *testing.T) {
	setupInfo := newSetupInfo(t)
	mount := config.GetWorkspaceMount(setupInfo)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(mount.Source, pkgconfig.IgnoreFileName), []byte("!\n"), 0o600),
	)
	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)
	stream := &mockStreamMountServer{}
	err = server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream)
	require.ErrorContains(t, err, "upload stopped")
	require.Zero(t, stream.content.Len())
}

// TestStreamMount_KeepsDockerlessBuildContext guards against #1108: the
// dockerless build fails if the ignore file strips its build context.
func TestStreamMount_KeepsDockerlessBuildContext(t *testing.T) {
	setupInfo := newSetupInfo(t)
	workspaceMount := config.GetWorkspaceMount(setupInfo)
	writeFiles(t, workspaceMount.Source,
		"run.sh",
		config.DevsyContextFeatureFolder+"/Dockerfile-without-features",
		config.DevsyContextFeatureFolder+"/feature/install.sh",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(workspaceMount.Source, pkgconfig.IgnoreFileName),
		[]byte("**/*.sh\n"+config.DevsyContextFeatureFolder+"\n"), 0o600))
	setupInfo.GeneratedBuildContext = workspaceMount.Source
	files := []string{
		config.DevsyContextFeatureFolder + "/Dockerfile-without-features",
		config.DevsyContextFeatureFolder + "/feature/install.sh",
	}
	for _, file := range files {
		// #nosec G304 -- temporary test fixture inside the test workspace.
		contents, err := os.ReadFile(filepath.Join(workspaceMount.Source, file))
		require.NoError(t, err)
		setupInfo.GeneratedBuildArtifacts = append(
			setupInfo.GeneratedBuildArtifacts,
			config.GeneratedBuildArtifact{
				Path:   filepath.Join(workspaceMount.Source, file),
				SHA256: fmt.Sprintf("%x", sha256.Sum256(contents)),
			},
		)
	}
	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)

	entries := streamMountEntries(t, server, workspaceMount)

	assert.Contains(t, entries, config.DevsyContextFeatureFolder+"/Dockerfile-without-features")
	assert.Contains(t, entries, config.DevsyContextFeatureFolder+"/feature/install.sh")
	assert.NotContains(t, entries, "run.sh")
}

func TestStreamMount_UnknownMountIsRejected(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server, err := newSetupServer(setupInfo)
	require.NoError(t, err)

	err = server.StreamMount(
		&tunnel.StreamMountRequest{Mount: "type=bind,src=/etc,dst=/etc"},
		&mockStreamMountServer{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not allowed")
}
