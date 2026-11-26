package agentworkspace

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/config"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestExistingContentPreparesAgentBinaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		validChecksum bool
	}{{"success", true}, {"checksum failure", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			payload := []byte("workspace-runtime-fixture")
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(payload)
				}),
			)
			t.Cleanup(server.Close)
			info := existingContentRuntime(t, server.URL, payload)
			marker := filepath.Join(info.ContentFolder, "user-data")
			require.NoError(t, os.WriteFile(marker, []byte("preserved"), 0o600))
			if !tc.validChecksum {
				info.Agent.Binaries["RUNTIME"][0].Checksum = hex.EncodeToString(
					make([]byte, sha256.Size),
				)
			}
			exists, err := InitContentFolder(context.Background(), info)
			require.True(t, exists)
			if tc.validChecksum {
				require.NoError(t, err)
				data, err := fs.ReadFile(os.DirFS(info.Origin), "binaries/runtime/runtime-fixture")
				require.NoError(t, err)
				require.Equal(t, payload, data)
			} else {
				require.ErrorContains(t, err, "checksum")
			}
			data, err := fs.ReadFile(os.DirFS(info.ContentFolder), "user-data")
			require.NoError(t, err)
			require.Equal(t, "preserved", string(data))
		})
	}
}

func existingContentRuntime(t *testing.T, url string, payload []byte) *provider.AgentWorkspaceInfo {
	t.Helper()
	home := t.TempDir()
	origin := filepath.Join(home, "contexts", config.DefaultContext, "workspaces", "binary-test")
	require.NoError(t, os.MkdirAll(origin, 0o750))
	sum := sha256.Sum256(payload)
	return &provider.AgentWorkspaceInfo{
		Origin: origin, ContentFolder: t.TempDir(),
		Workspace: &provider.Workspace{Context: config.DefaultContext, ID: "binary-test"},
		Agent: provider.ProviderAgentConfig{
			DataPath: home,
			Binaries: map[string][]*provider.ProviderBinary{"RUNTIME": {{
				OS: runtime.GOOS, Arch: runtime.GOARCH, Path: url, Name: "runtime-fixture",
				Checksum: hex.EncodeToString(sum[:]),
			}}},
		},
	}
}

func TestBinaryPreparationFailureCleanup(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			info, sshConfig := binaryCleanupWorkspace(t, existing)
			err := prepareWorkspace(
				context.Background(),
				prepareWorkspaceParams{workspaceInfo: info},
			)
			require.ErrorContains(t, err, "checksum")
			initErr := fmt.Errorf("initialize workspace: %w", err)
			cmd := &UpCmd{}
			require.ErrorIs(t, cmd.handleInitError(initErr, info), initErr)

			if !existing {
				require.NoDirExists(t, info.Origin)
				require.NoDirExists(t, info.ContentFolder)
				data, err := fs.ReadFile(
					os.DirFS(filepath.Dir(info.Workspace.SSHConfigPath)),
					"ssh_config",
				)
				require.NoError(t, err)
				require.NotContains(t, string(data), "binary-test")
				return
			}
			for _, file := range []struct{ dir, name, want string }{
				{info.Origin, provider.WorkspaceConfigFile, "workspace record"},
				{info.ContentFolder, "user-data", "preserved"},
				{filepath.Dir(info.Workspace.SSHConfigPath), "ssh_config", string(sshConfig)},
			} {
				data, err := fs.ReadFile(os.DirFS(file.dir), file.name)
				require.NoError(t, err)
				require.Equal(t, file.want, string(data))
			}
		})
	}
}

func TestLocalFolderBinaryFailurePreservesUserContent(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, managedPath := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("existing=%t/managed_path=%t", existing, managedPath),
				func(t *testing.T) {
					info, sshConfig := binaryCleanupWorkspace(t, true)
					info.WorkspaceWasExisting = existing
					if !managedPath {
						info.ContentFolder = t.TempDir()
						require.NoError(t, os.WriteFile(
							filepath.Join(
								info.ContentFolder,
								"user-data",
							),
							[]byte("preserved"),
							0o600,
						))
					}
					info.Workspace.Source.LocalFolder = info.ContentFolder
					err := prepareWorkspace(
						context.Background(),
						prepareWorkspaceParams{workspaceInfo: info},
					)
					require.ErrorContains(t, err, "checksum")
					require.ErrorIs(t, (&UpCmd{}).handleInitError(err, info), err)
					data, readErr := os.ReadFile(filepath.Join(info.ContentFolder, "user-data"))
					require.NoError(t, readErr)
					require.Equal(t, "preserved", string(data))
					data, readErr = os.ReadFile(info.Workspace.SSHConfigPath)
					require.NoError(t, readErr)
					if existing {
						record, recordErr := os.ReadFile(
							filepath.Join(info.Origin, provider.WorkspaceConfigFile),
						)
						require.NoError(t, recordErr)
						require.Equal(t, "workspace record", string(record))
						require.Equal(t, sshConfig, data)
					} else {
						require.NoDirExists(t, info.Origin)
						require.NotContains(t, string(data), "binary-test")
					}
				},
			)
		}
	}
}

func binaryCleanupWorkspace(t *testing.T, existing bool) (*provider.AgentWorkspaceInfo, []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	payload := []byte("invalid-checksum-runtime")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	info := existingContentRuntime(t, server.URL, payload)
	info.Agent.DataPath = home
	var err error
	info.Origin, err = provider.GetWorkspaceDir(info.Workspace.Context, info.Workspace.ID)
	require.NoError(t, err)
	info.ContentFolder, err = provider.GetWorkspaceContentDir(
		info.Workspace.Context,
		info.Workspace.ID,
	)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(info.Origin, 0o750))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(info.Origin, provider.WorkspaceConfigFile),
			[]byte("workspace record"),
			0o600,
		),
	)
	if existing {
		require.NoError(t, os.MkdirAll(info.ContentFolder, 0o750))
		require.NoError(
			t,
			os.WriteFile(
				filepath.Join(info.ContentFolder, "user-data"),
				[]byte("preserved"),
				0o600,
			),
		)
	}
	info.Workspace.SSHConfigPath = filepath.Join(t.TempDir(), "ssh_config")
	require.NoError(t, ssh.ConfigureSSHConfig(ssh.SSHConfigParams{
		SSHConfigPath: info.Workspace.SSHConfigPath,
		Context:       info.Workspace.Context, Workspace: info.Workspace.ID, User: "root",
	}))
	sshConfig, err := fs.ReadFile(
		os.DirFS(filepath.Dir(info.Workspace.SSHConfigPath)),
		"ssh_config",
	)
	require.NoError(t, err)
	info.Agent.Binaries["RUNTIME"][0].Checksum = hex.EncodeToString(make([]byte, sha256.Size))
	return info, sshConfig
}

func TestReusedWorkspaceWithoutContentPreservesState(t *testing.T) {
	info, sshConfig := binaryCleanupWorkspace(t, false)
	info.Workspace.UID = "same-workspace-uid"
	info.Agent.Local = config.BoolTrue
	data, err := json.Marshal(info)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(info.Origin, provider.WorkspaceConfigFile), data, 0o600),
	)
	encoded, err := compress.Compress(string(data))
	require.NoError(t, err)
	shouldExit, loaded, err := agent.WriteWorkspaceInfoAndDeleteOld(
		encoded,
		func(*provider.AgentWorkspaceInfo) error {
			t.Fatal("same-UID workspace must not be replaced")
			return nil
		},
	)
	require.NoError(t, err)
	require.False(t, shouldExit)
	require.NotNil(t, loaded)
	record, err := fs.ReadFile(os.DirFS(loaded.Origin), provider.WorkspaceConfigFile)
	require.NoError(t, err)
	err = prepareWorkspace(context.Background(), prepareWorkspaceParams{workspaceInfo: loaded})
	require.ErrorContains(t, err, "checksum")
	cmd := &UpCmd{}
	require.ErrorIs(t, cmd.handleInitError(err, loaded), err)
	got, err := fs.ReadFile(os.DirFS(loaded.Origin), provider.WorkspaceConfigFile)
	require.NoError(t, err)
	require.Equal(t, record, got)
	got, err = fs.ReadFile(os.DirFS(filepath.Dir(loaded.Workspace.SSHConfigPath)), "ssh_config")
	require.NoError(t, err)
	require.Equal(t, sshConfig, got)
	require.NoDirExists(t, loaded.ContentFolder)
}

func TestSourcePreparationFailureCleanupAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		existing, fallbackConfig bool
	}{
		{name: "new content"},
		{name: "existing content", existing: true},
		{name: "fallback config", fallbackConfig: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, sshConfig := binaryCleanupWorkspace(t, tc.existing)
			info.Agent.Binaries = nil
			info.WorkspaceWasExisting = true
			info.Workspace.Source.LocalFolder = "host-source"
			info.CLIOptions.Recreate = tc.existing || tc.fallbackConfig
			if tc.fallbackConfig {
				info.LastDevContainerConfig = &devcontainerconfig.DevContainerConfigWithPath{
					Path: ".devcontainer.json", Config: &devcontainerconfig.DevContainerConfig{},
				}
			}
			uploadErr := errors.New("source stream interrupted")
			var archive bytes.Buffer
			tw := tar.NewWriter(&archive)
			require.NoError(t, tw.WriteHeader(&tar.Header{
				Name: "source.txt", Mode: 0o600, Size: 6,
			}))
			_, err := tw.Write([]byte("source"))
			require.NoError(t, err)
			require.NoError(t, tw.Close())
			client := &sourceRetryClient{archive: archive.Bytes(), firstError: uploadErr}
			params := prepareWorkspaceParams{
				workspaceInfo: info, client: client, logger: workspaceTestLogger{},
			}
			err = prepareWorkspace(context.Background(), params)
			require.ErrorIs(t, err, uploadErr)
			require.ErrorIs(t, (&UpCmd{}).handleInitError(err, info), uploadErr)
			for _, file := range []struct{ dir, name, want string }{
				{info.Origin, provider.WorkspaceConfigFile, "workspace record"},
				{filepath.Dir(info.Workspace.SSHConfigPath), "ssh_config", string(sshConfig)},
			} {
				data, err := fs.ReadFile(os.DirFS(file.dir), file.name)
				require.NoError(t, err)
				require.Equal(t, file.want, string(data))
			}
			if tc.existing {
				data, err := fs.ReadFile(os.DirFS(info.ContentFolder), "user-data")
				require.NoError(t, err)
				require.Equal(t, "preserved", string(data))
			} else {
				require.NoDirExists(t, info.ContentFolder)
			}
			require.NoError(t, prepareWorkspace(context.Background(), params))
			require.Equal(t, 2, client.calls)
			data, err := fs.ReadFile(os.DirFS(info.ContentFolder), "source.txt")
			require.NoError(t, err)
			require.Equal(t, "source", string(data))
		})
	}
}

type sourceRetryClient struct {
	tunnel.TunnelClient
	archive    []byte
	firstError error
	calls      int
}

func (c *sourceRetryClient) StreamWorkspace(
	context.Context, *tunnel.Empty, ...grpc.CallOption,
) (grpc.ServerStreamingClient[tunnel.Chunk], error) {
	c.calls++
	if c.calls == 1 {
		// A complete file arrives before the stream fails reading the next header.
		return &sourceRetryStream{data: c.archive[:1024], err: c.firstError}, nil
	}
	return &sourceRetryStream{data: c.archive, err: io.EOF}, nil
}

type sourceRetryStream struct {
	grpc.ClientStream
	data []byte
	err  error
}

func (s *sourceRetryStream) Recv() (*tunnel.Chunk, error) {
	if len(s.data) == 0 {
		return nil, s.err
	}
	data := s.data
	s.data = nil
	return &tunnel.Chunk{Content: data}, nil
}
