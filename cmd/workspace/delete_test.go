package workspace

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteCmd_Aliases(t *testing.T) {
	deleteCmd := NewDeleteCmd(&flags.GlobalFlags{})

	assert.ElementsMatch(t, []string{aliasRm, aliasDown}, deleteCmd.Aliases)
}

func TestDeleteCmd_Resolution(t *testing.T) {
	workspaceCmd := NewWorkspaceCmd(&flags.GlobalFlags{})

	testCases := []struct {
		name string
		args []string
	}{
		{
			name: "canonical delete",
			args: []string{"delete"},
		},
		{
			name: "rm alias",
			args: []string{aliasRm},
		},
		{
			name: "down alias",
			args: []string{aliasDown},
		},
		{
			name: "down alias with positional workspace",
			args: []string{aliasDown, "my-workspace"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, remainingArgs, err := workspaceCmd.Find(tc.args)
			require.NoError(t, err)
			require.NotNil(t, resolved)
			assert.Equal(t, "delete", resolved.Name())
			if len(tc.args) > 1 {
				assert.Equal(t, tc.args[1:], remainingArgs)
			} else {
				assert.Empty(t, remainingArgs)
			}
		})
	}
}

func TestDeleteCmd_HelpExposesDownAlias(t *testing.T) {
	deleteCmd := NewDeleteCmd(&flags.GlobalFlags{})

	var buf bytes.Buffer
	deleteCmd.SetOut(&buf)
	err := deleteCmd.Help()
	require.NoError(t, err)

	helpOutput := buf.String()
	assert.Contains(t, helpOutput, "Aliases:")
	assert.Contains(t, helpOutput, aliasDown)
	assert.Contains(t, helpOutput, aliasRm)
}

func TestDeleteCmd_Completion(t *testing.T) {
	workspaceCmd := NewWorkspaceCmd(&flags.GlobalFlags{})

	var buf bytes.Buffer
	err := workspaceCmd.GenBashCompletion(&buf)
	require.NoError(t, err)

	completionOutput := buf.String()
	assert.Contains(t, completionOutput, fmt.Sprintf("%q", aliasDown))
	assert.Contains(t, completionOutput, fmt.Sprintf("%q", aliasRm))
	assert.Contains(t, completionOutput, `"delete"`)
}
