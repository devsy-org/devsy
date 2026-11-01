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
		ID:           "node-js",
		UID:          "uid-1",
		DisplayName:  "Node JS",
		SSHHostAlias: "node",
	}

	encoded, err := json.Marshal(newWorkspaceListEntry(ws))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, "node-js", got["id"])
	assert.Equal(t, "uid-1", got["uid"])
	assert.Equal(t, "Node JS", got["displayName"])
	assert.Equal(t, "node", got["sshHostAlias"])
	assert.Equal(t, "node.devsy", got["preferredSSHHost"])
	assert.Equal(t, "node-js.devsy", got["canonicalSSHHost"])
}

func TestWorkspaceListEntryFallsBackToID(t *testing.T) {
	ws := &provider.Workspace{ID: "node-js"}

	encoded, err := json.Marshal(newWorkspaceListEntry(ws))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, "node-js", got["displayName"])
	assert.Equal(t, "", got["sshHostAlias"])
	assert.Equal(t, "node-js.devsy", got["preferredSSHHost"])
	assert.Equal(t, "node-js.devsy", got["canonicalSSHHost"])
}

func TestWorkspaceListNameShowsLabelOnlyWhenDifferent(t *testing.T) {
	assert.Equal(t, "node-js", workspaceListName(&provider.Workspace{ID: "node-js"}))
	assert.Equal(
		t,
		"Node JS (node-js)",
		workspaceListName(&provider.Workspace{ID: "node-js", DisplayName: "Node JS"}),
	)
}

func TestDescribeRowsShowLabelAndHosts(t *testing.T) {
	plain := describeRows(&provider.Workspace{ID: "node-js"}, "Running")
	assert.NotContains(t, rowFields(plain), "Label")
	assert.NotContains(t, rowFields(plain), "Canonical SSH Host")
	assert.Contains(t, rowFields(plain), "SSH Host")

	aliased := describeRows(&provider.Workspace{
		ID:           "node-js",
		DisplayName:  "Node JS",
		SSHHostAlias: "node",
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
