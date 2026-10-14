package tunnelserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	mountRPCBindType        = "bind"
	mountRPCWorkspaceTarget = "/workspace"
	mountRPCDependencyFile  = "node_modules/lib/index.js"
	mountRPCPrefixFile      = "a-first.txt"
)

func localMountRPCClient(
	t *testing.T,
	server *tunnelServer,
) (context.Context, tunnel.TunnelClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverR, serverW) }()
	t.Cleanup(func() {
		cancel()
		_ = clientW.Close()
		_ = serverW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("local tunnel did not stop")
		}
	})
	client, err := NewTunnelClient(clientR, clientW)
	require.NoError(t, err)
	return ctx, client
}

type countedMountStream struct {
	tunnel.Tunnel_StreamMountClient
	bytes  int
	chunks int
	done   chan error
}

func (s *countedMountStream) Recv() (*tunnel.Chunk, error) {
	chunk, err := s.Tunnel_StreamMountClient.Recv()
	if chunk != nil {
		s.bytes += len(chunk.Content)
		s.chunks++
	}
	if err != nil && s.done != nil {
		s.done <- err
	}
	return chunk, err
}

func extractMountRPC(
	t *testing.T,
	ctx context.Context,
	client tunnel.TunnelClient,
	mount *config.Mount,
) (string, int) {
	t.Helper()
	stream, err := client.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: mount.String()})
	require.NoError(t, err)
	counted := &countedMountStream{Tunnel_StreamMountClient: stream, done: make(chan error, 1)}
	dest := t.TempDir()
	require.NoError(t, extract.Extract(NewStreamReader(counted), dest))
	// Extract stops at the tar terminator; wait for the receiver to observe the final RPC status.
	select {
	case err = <-counted.done:
		require.ErrorIs(t, err, io.EOF)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return dest, counted.bytes
}

func sparseMountFixture(t *testing.T, root, name string) {
	t.Helper()
	filePath := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0o750))
	// #nosec G304 -- fixture path is assembled from test-controlled names under t.TempDir.
	file, err := os.Create(filePath)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(20*1024*1024))
	require.NoError(t, file.Close())
}

func TestStreamMountRPC_ExtractsFilteredWorkspaceAndIndependentMount(t *testing.T) {
	workspace, other := t.TempDir(), t.TempDir()
	writeFiles(
		t,
		workspace,
		"keep.txt",
		".git/config",
		"docs/a.md",
		"docs/a.tgz",
		"web/node_modules/lib/index.js",
		"build/keep.txt",
		"build/remove.txt",
	)
	sparseMountFixture(t, workspace, "big/blob.bin")
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(workspace, pkgconfig.IgnoreFileName),
			[]byte("big/\n**/node_modules/\ndocs/*.tgz\nbuild/\n!build/keep.txt\n"),
			0o600,
		),
	)
	writeFiles(t, other, mountRPCDependencyFile, "big/keep.txt", "docs/a.tgz")
	workspaceMount := &config.Mount{
		Type:   mountRPCBindType,
		Source: workspace,
		Target: "/workspaces/project",
	}
	otherMount := &config.Mount{Type: mountRPCBindType, Source: other, Target: "/additional"}
	ctx, client := localMountRPCClient(
		t,
		New(
			WithMounts([]*config.Mount{workspaceMount, otherMount}),
			WithWorkspaceMount(workspaceMount),
		),
	)

	dest, sent := extractMountRPC(t, ctx, client, workspaceMount)
	require.Less(t, sent, 1024*1024, "excluded 20 MiB file must not cross the RPC stream")
	for _, name := range []string{"keep.txt", ".git/config", "docs/a.md", "build/keep.txt"} {
		// #nosec G304 -- names are fixed test fixtures inside the temporary extraction destination.
		content, err := os.ReadFile(filepath.Join(dest, name))
		require.NoError(t, err)
		require.Equal(t, name, string(content))
	}
	for _, name := range []string{"big/blob.bin", "web/node_modules/lib/index.js", "docs/a.tgz", "build/remove.txt"} {
		require.NoFileExists(t, filepath.Join(dest, name))
	}
	otherDest, _ := extractMountRPC(t, ctx, client, otherMount)
	for _, name := range []string{mountRPCDependencyFile, "big/keep.txt", "docs/a.tgz"} {
		require.FileExists(t, filepath.Join(otherDest, name))
	}
}

func TestStreamMountRPC_ExtractsOnlyManifestedNestedBuildArtifacts(t *testing.T) {
	root := t.TempDir()
	contextRoot := filepath.Join(root, ".devcontainer")
	dockerfile := ".devcontainer/" + config.DevsyContextFeatureFolder + "/Dockerfile-without-features"
	install := ".devcontainer/" + config.DevsyContextFeatureFolder + "/feature/install.sh"
	lookalike := "private/" + config.DevsyContextFeatureFolder + "/secret.key"
	unmanifested := ".devcontainer/" + config.DevsyContextFeatureFolder + "/private.key"
	writeFiles(t, root, "keep.txt", dockerfile, install, lookalike, unmanifested, "scripts/run.sh")
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, pkgconfig.IgnoreFileName),
			[]byte(".devcontainer/\n**/.devsy-internal/\n**/*.sh\n"),
			0o600,
		),
	)
	mount := &config.Mount{Type: mountRPCBindType, Source: root, Target: mountRPCWorkspaceTarget}
	server := New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount))
	server.generatedBuildContext = contextRoot
	for _, name := range []string{dockerfile, install} {
		server.generatedBuildArtifacts = append(
			server.generatedBuildArtifacts,
			config.GeneratedBuildArtifact{
				Path:   filepath.Join(root, name),
				SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(name))),
			},
		)
	}
	ctx, client := localMountRPCClient(t, server)
	dest, _ := extractMountRPC(t, ctx, client, mount)
	for _, name := range []string{"keep.txt", dockerfile, install} {
		require.FileExists(t, filepath.Join(dest, name))
	}
	for _, name := range []string{lookalike, unmanifested, "scripts/run.sh"} {
		require.NoFileExists(t, filepath.Join(dest, name))
	}
}

func TestStreamMountRPC_ProtectedArtifactUsesPreflightContents(t *testing.T) {
	root := t.TempDir()
	sparseMountFixture(t, root, "000-prefix.bin")
	name := "z-build/" + config.DevsyContextFeatureFolder + "/Dockerfile-without-features"
	approved := []byte("FROM approved\n")
	writeFiles(t, root, name)
	artifactPath := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(artifactPath, approved, 0o600))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, pkgconfig.IgnoreFileName), []byte("z-build/\n"), 0o600),
	)
	mount := &config.Mount{Type: mountRPCBindType, Source: root, Target: mountRPCWorkspaceTarget}
	server := New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount))
	server.generatedBuildContext = filepath.Join(root, "z-build")
	server.generatedBuildArtifacts = []config.GeneratedBuildArtifact{
		{Path: artifactPath, SHA256: fmt.Sprintf("%x", sha256.Sum256(approved))},
	}
	ctx, client := localMountRPCClient(t, server)
	stream, err := client.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: mount.String()})
	require.NoError(t, err)
	first, err := stream.Recv()
	require.NoError(t, err)
	// The earlier entry blocks the sender after preflight and before the artifact.
	require.NoError(t, os.WriteFile(artifactPath, []byte("private replacement secret\n"), 0o600))
	dest := t.TempDir()
	require.NoError(
		t,
		extract.Extract(
			io.MultiReader(bytes.NewReader(first.Content), NewStreamReader(stream)),
			dest,
		),
	)
	// #nosec G304 -- names are fixed test fixtures inside the temporary extraction destination.
	content, err := os.ReadFile(filepath.Join(dest, name))
	require.NoError(t, err)
	require.Equal(t, approved, content)
}

func TestStreamMountRPC_InvalidPolicyFailsBeforeChunks(t *testing.T) {
	for _, policy := range []string{"[", "!"} {
		t.Run(policy, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, "private.txt")
			require.NoError(
				t,
				os.WriteFile(filepath.Join(root, pkgconfig.IgnoreFileName), []byte(policy), 0o600),
			)
			mount := &config.Mount{
				Type:   mountRPCBindType,
				Source: root,
				Target: mountRPCWorkspaceTarget,
			}
			ctx, client := localMountRPCClient(
				t,
				New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount)),
			)
			stream, err := client.StreamMount(
				ctx,
				&tunnel.StreamMountRequest{Mount: mount.String()},
			)
			require.NoError(t, err)
			counted := &countedMountStream{
				Tunnel_StreamMountClient: stream,
				done:                     make(chan error, 1),
			}
			err = extract.Extract(NewStreamReader(counted), t.TempDir())
			require.ErrorContains(t, err, pkgconfig.IgnoreFileName)
			require.Zero(t, counted.chunks)
			require.Zero(t, counted.bytes)
		})
	}
}

func TestStreamMountRPC_UnreadablePolicyFailsBeforeChunks(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "private.txt")
	// A directory at the policy path fails on every OS, including privileged test runners.
	require.NoError(t, os.Mkdir(filepath.Join(root, pkgconfig.IgnoreFileName), 0o750))
	mount := &config.Mount{Type: mountRPCBindType, Source: root, Target: mountRPCWorkspaceTarget}
	ctx, client := localMountRPCClient(
		t,
		New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount)),
	)
	stream, err := client.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: mount.String()})
	require.NoError(t, err)
	counted := &countedMountStream{Tunnel_StreamMountClient: stream, done: make(chan error, 1)}
	err = extract.Extract(NewStreamReader(counted), t.TempDir())
	require.ErrorContains(t, err, pkgconfig.IgnoreFileName)
	require.Zero(t, counted.chunks)
	require.Zero(t, counted.bytes)
}

func TestStreamMountRPC_MissingSourceFailsExtraction(t *testing.T) {
	mount := &config.Mount{
		Type:   mountRPCBindType,
		Source: filepath.Join(t.TempDir(), "missing-source"),
		Target: mountRPCWorkspaceTarget,
	}
	ctx, client := localMountRPCClient(
		t,
		New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount)),
	)
	stream, err := client.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: mount.String()})
	require.NoError(t, err)
	require.Error(t, extract.Extract(NewStreamReader(stream), t.TempDir()))
}

func TestStreamMountRPC_SourceReadFailureAfterPayloadFailsExtraction(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires enforced POSIX directory permissions")
	}
	root := t.TempDir()
	writeFiles(t, root, mountRPCPrefixFile, "z-unreadable/hidden.txt")
	// Align header, payload and success terminator to the RPC buffer boundary:
	// closing a failed tar would otherwise flush a complete-looking archive.
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, mountRPCPrefixFile),
			bytes.Repeat([]byte("a"), 2*10*1024-3*512),
			0o600,
		),
	)
	denied := filepath.Join(root, "z-unreadable")
	require.NoError(t, os.Chmod(denied, 0))
	t.Cleanup(func() {
		// #nosec G302 -- restore traversal permissions on a temporary fixture directory for cleanup.
		require.NoError(t, os.Chmod(denied, 0o700))
	})
	if _, err := os.ReadDir(denied); err == nil {
		t.Skip("filesystem does not enforce unreadable directory permissions")
	}
	mount := &config.Mount{Type: mountRPCBindType, Source: root, Target: mountRPCWorkspaceTarget}
	ctx, client := localMountRPCClient(
		t,
		New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount)),
	)
	stream, err := client.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: mount.String()})
	require.NoError(t, err)
	dest := t.TempDir()
	err = extract.Extract(NewStreamReader(stream), dest)
	require.Error(t, err, "an archive that stopped after earlier payload must not look complete")
	require.ErrorContains(t, err, "z-unreadable")
	info, statErr := os.Stat(filepath.Join(dest, mountRPCPrefixFile))
	require.NoError(t, statErr)
	require.Positive(t, info.Size(), "earlier payload must actually reach the receiver")
}

func TestStreamMountRPC_CancellationCannotExtractTruncatedArchive(t *testing.T) {
	root := t.TempDir()
	sparseMountFixture(t, root, "blob.bin")
	mount := &config.Mount{Type: mountRPCBindType, Source: root, Target: mountRPCWorkspaceTarget}
	ctx, client := localMountRPCClient(
		t,
		New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount)),
	)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.StreamMount(streamCtx, &tunnel.StreamMountRequest{Mount: mount.String()})
	require.NoError(t, err)
	first, err := stream.Recv()
	require.NoError(t, err)
	require.NotEmpty(t, first.Content)
	cancel()
	require.Error(
		t,
		extract.Extract(
			io.MultiReader(bytes.NewReader(first.Content), NewStreamReader(stream)),
			t.TempDir(),
		),
	)
	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))
}
