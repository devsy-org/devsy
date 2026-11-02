package workspace

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportWorkspace_SnapshotRefRestoresSourceAndDevContainerSource(t *testing.T) {
	srcStr, devContainerSource, err := snapshotpkg.RestoreComposition(
		"ghcr.io/acme/s:my-ws-20260731150405-abcxyz",
	)
	require.NoError(t, err)

	parsedSource := provider.ParseWorkspaceSource(srcStr)
	require.NotNil(t, parsedSource)

	assert.Equal(t, provider.WorkspaceSourceSnapshot, parsedSource.Type())
	assert.Equal(t, "ghcr.io/acme/s:my-ws-20260731150405-abcxyz", parsedSource.Snapshot)
	assert.Equal(t, "image:ghcr.io/acme/s:my-ws-20260731150405-abcxyz-fs", devContainerSource)
}

func TestImportWorkspace_SnapshotRefBadRef(t *testing.T) {
	_, _, err := snapshotpkg.RestoreComposition("invalid-ref")
	require.Error(t, err)
}

func TestImportCmdExecuteParseFailureKeepsStatusEnvelope(t *testing.T) {
	cmd := &ImportCmd{
		GlobalFlags: &flags.GlobalFlags{ResultFormat: formatJSON},
		Data:        "not json",
	}
	var execErr error
	out := captureStdout(t, func() {
		execErr = cmd.execute(t.Context())
	})
	require.Error(t, execErr)
	require.Contains(t, out, `"kind":"status"`)
	require.Contains(t, out, "decode workspace data")
}

const (
	importTestContext = "default"
	importTestID      = "imported-ws"
	importTestUID     = "uid-imported"

	importTestTheirsLabel = "Theirs"
	importTestTheirsAlias = "theirs"
)

func setupImportTestHome(t *testing.T) *config.Config {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	return &config.Config{DefaultContext: importTestContext}
}

func importArchive(t *testing.T, ws *provider.Workspace) string {
	t.Helper()

	content, err := json.Marshal(ws)
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     provider.WorkspaceConfigFile,
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	return base64.RawStdEncoding.EncodeToString(buf.Bytes())
}

func runImportWorkspace(
	t *testing.T,
	devsyConfig *config.Config,
	imported *provider.Workspace,
) *provider.Workspace {
	t.Helper()

	cmd := &ImportCmd{WorkspaceID: importTestID, MachineID: "m", ProviderID: "p"}
	exportConfig := &provider.ExportConfig{
		Workspace: &provider.ExportWorkspaceConfig{Data: importArchive(t, imported)},
	}
	require.NoError(t, cmd.importWorkspace(devsyConfig, exportConfig))

	got, err := provider.LoadWorkspaceConfig(importTestContext, importTestID)
	require.NoError(t, err)
	return got
}

func saveImportTestWorkspace(t *testing.T, ws *provider.Workspace) {
	t.Helper()

	ws.Context = importTestContext
	require.NoError(t, provider.SaveWorkspaceConfig(ws))
}

func TestImportWorkspace_RestoresLocalPresentationForSameUID(t *testing.T) {
	devsyConfig := setupImportTestHome(t)
	saveImportTestWorkspace(t, &provider.Workspace{
		ID:           importTestID,
		UID:          importTestUID,
		DisplayName:  "Mine",
		SSHHostAlias: "mine",
	})

	got := runImportWorkspace(t, devsyConfig, &provider.Workspace{
		ID:           importTestID,
		UID:          importTestUID,
		DisplayName:  importTestTheirsLabel,
		SSHHostAlias: importTestTheirsAlias,
	})

	assert.Equal(t, "Mine", got.DisplayName)
	assert.Equal(t, "mine", got.SSHHostAlias)
	assert.Equal(t, importTestUID, got.UID)
}

func TestImportWorkspace_LocalWithoutPresentationClearsImportedForSameUID(t *testing.T) {
	devsyConfig := setupImportTestHome(t)
	saveImportTestWorkspace(t, &provider.Workspace{ID: importTestID, UID: importTestUID})

	got := runImportWorkspace(t, devsyConfig, &provider.Workspace{
		ID:           importTestID,
		UID:          importTestUID,
		DisplayName:  importTestTheirsLabel,
		SSHHostAlias: importTestTheirsAlias,
	})

	assert.Empty(t, got.DisplayName)
	assert.Empty(t, got.SSHHostAlias)
}

func TestImportWorkspace_KeepsValidPresentationForNewWorkspace(t *testing.T) {
	devsyConfig := setupImportTestHome(t)

	got := runImportWorkspace(t, devsyConfig, &provider.Workspace{
		ID:           importTestID,
		UID:          importTestUID,
		DisplayName:  importTestTheirsLabel,
		SSHHostAlias: importTestTheirsAlias,
	})

	assert.Equal(t, importTestTheirsLabel, got.DisplayName)
	assert.Equal(t, importTestTheirsAlias, got.SSHHostAlias)
}

func TestImportWorkspace_DropsConflictingPresentationForNewWorkspace(t *testing.T) {
	devsyConfig := setupImportTestHome(t)
	saveImportTestWorkspace(t, &provider.Workspace{
		ID:           "web",
		UID:          "uid-web",
		DisplayName:  "Web",
		SSHHostAlias: "backend",
	})

	got := runImportWorkspace(t, devsyConfig, &provider.Workspace{
		ID:           importTestID,
		UID:          importTestUID,
		DisplayName:  "web",
		SSHHostAlias: "backend",
	})

	assert.Empty(t, got.DisplayName)
	assert.Empty(t, got.SSHHostAlias)
}

func TestImportWorkspace_DropsOnlyInvalidImportedFields(t *testing.T) {
	devsyConfig := setupImportTestHome(t)

	got := runImportWorkspace(t, devsyConfig, &provider.Workspace{
		ID:           importTestID,
		UID:          importTestUID,
		DisplayName:  "Good Label",
		SSHHostAlias: "bad.devsy",
	})

	assert.Equal(t, "Good Label", got.DisplayName)
	assert.Empty(t, got.SSHHostAlias)
}
