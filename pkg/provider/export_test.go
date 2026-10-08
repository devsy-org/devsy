package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/stretchr/testify/require"
)

func TestExportConfig_SnapshotRefRoundTrips(t *testing.T) {
	cfg := &ExportConfig{SnapshotRef: testProviderSnapRef}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got ExportConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, cfg.SnapshotRef, got.SnapshotRef)
}

func TestExportWorkspace_ArchiveRoundTrip(t *testing.T) {
	t.Setenv("DEVSY_HOME", t.TempDir())
	workspace := &Workspace{ID: "archive-round-trip", UID: "archive-uid", Context: "default"}
	require.NoError(t, SaveWorkspaceConfig(workspace))
	dir, err := GetWorkspaceDir(workspace.Context, workspace.ID)
	require.NoError(t, err)
	for _, name := range []string{"source/large.bin", "cache/stale", "cacheable/keep.txt", "settings.json"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	exported, err := ExportWorkspace(workspace.Context, workspace.ID)
	require.NoError(t, err)
	require.Equal(t, workspace.UID, exported.UID)
	raw, err := base64.RawStdEncoding.DecodeString(exported.Data)
	require.NoError(t, err)
	restored := t.TempDir()
	require.NoError(t, extract.Extract(bytes.NewReader(raw), restored))
	for _, name := range []string{"source/large.bin", "cache/stale"} {
		require.NoFileExists(t, filepath.Join(restored, name))
	}
	for _, name := range []string{"cacheable/keep.txt", "settings.json"} {
		// #nosec G304 -- fixed fixture names beneath the temporary extraction root.
		content, readErr := os.ReadFile(filepath.Join(restored, name))
		require.NoError(t, readErr)
		require.Equal(t, name, string(content))
	}
	require.FileExists(t, filepath.Join(restored, WorkspaceConfigFile))
}
