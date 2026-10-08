package agentworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
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
