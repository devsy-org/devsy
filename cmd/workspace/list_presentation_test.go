package workspace

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceListEntryAddsPresentationFields(t *testing.T) {
	ws := &provider.Workspace{
		ID:           testWorkspaceName,
		UID:          "uid-1",
		DisplayName:  testDisplayName,
		SSHHostAlias: testSSHAlias,
	}

	encoded, err := json.Marshal(provider.NewWorkspaceListEntry(ws))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, testWorkspaceName, got["id"])
	assert.Equal(t, "uid-1", got["uid"])
	assert.Equal(t, testDisplayName, got["displayName"])
	assert.Equal(t, testDisplayName, got["effectiveDisplayName"])
	assert.Equal(t, testSSHAlias, got["sshHostAlias"])
	assert.Equal(t, "node.devsy", got["preferredSSHHost"])
	assert.Equal(t, "node-js.devsy", got["canonicalSSHHost"])
}

func TestWorkspaceListEntryRawDisplayNameEmptyWhenUnset(t *testing.T) {
	ws := &provider.Workspace{ID: testWorkspaceName}

	encoded, err := json.Marshal(provider.NewWorkspaceListEntry(ws))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, "", got["displayName"])
	assert.Equal(t, testWorkspaceName, got["effectiveDisplayName"])
	assert.Equal(t, "", got["sshHostAlias"])
	assert.Equal(t, "node-js.devsy", got["preferredSSHHost"])
	assert.Equal(t, "node-js.devsy", got["canonicalSSHHost"])
}

func TestWorkspaceListEntryKeepsPlatformNameSeparateFromRawOverride(t *testing.T) {
	ws := &provider.Workspace{
		ID:          testWorkspaceName,
		DisplayName: testDisplayName,
		Platform:    &provider.PlatformMetadata{DisplayName: "Team Workspace"},
	}

	encoded, err := json.Marshal(provider.NewWorkspaceListEntry(ws))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, testDisplayName, got["displayName"])
	assert.Equal(t, "Team Workspace", got["effectiveDisplayName"])
	pro, ok := got["pro"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Team Workspace", pro["displayName"])
}

func TestWorkspaceListNameShowsLabelOnlyWhenDifferent(t *testing.T) {
	assert.Equal(
		t,
		testWorkspaceName,
		workspaceListName(&provider.Workspace{ID: testWorkspaceName}),
	)
	assert.Equal(
		t,
		"Node JS (node-js)",
		workspaceListName(&provider.Workspace{ID: testWorkspaceName, DisplayName: testDisplayName}),
	)
}

func TestDescribeRowsShowLabelAndHosts(t *testing.T) {
	plain := describeRows(&provider.Workspace{ID: testWorkspaceName}, "Running")
	assert.NotContains(t, rowFields(plain), "Label")
	assert.NotContains(t, rowFields(plain), "Canonical SSH Host")
	assert.Contains(t, rowFields(plain), "SSH Host")

	aliased := describeRows(&provider.Workspace{
		ID:           testWorkspaceName,
		DisplayName:  testDisplayName,
		SSHHostAlias: testSSHAlias,
	}, "Running")
	assert.Contains(t, rowFields(aliased), "Label")
	assert.Contains(t, rowFields(aliased), "Canonical SSH Host")
}

func rowFields(rows [][]string) []string {
	fields := make([]string, 0, len(rows))
	for _, row := range rows {
		fields = append(fields, row[0])
	}
	return fields
}
