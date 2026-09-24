package workspace

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/workspacejournal"
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

func TestDeleteCmd_DeleteResolvedJournalsUnderSelectedWorkspace(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	fake := &fakeWorkspaceClient{
		workspace: "chosen",
		context:   testContext,
		provider:  testProvider,
		config:    &provider.Workspace{ID: "chosen", Context: testContext},
	}
	cmd := &DeleteCmd{
		GlobalFlags:   &flags.GlobalFlags{ResultFormat: formatPlain},
		DeleteOptions: client2.DeleteOptions{Force: true},
	}
	devsyConfig := &config.Config{
		DefaultContext: testContext,
		Contexts:       map[string]*config.ContextConfig{testContext: {}},
	}
	reporter, err := newWorkspaceStatusReporter(formatPlain, os.Stdout, false)
	require.NoError(t, err)

	captureStdout(t, func() {
		require.NoError(t, cmd.deleteResolved(t.Context(), reporter, devsyConfig, fake))
	})
	require.True(t, fake.deleted)

	dir, err := workspacejournal.DefaultDir()
	require.NoError(t, err)
	events, err := workspacejournal.Read(dir, "chosen", workspacejournal.DefaultLimit)
	require.NoError(t, err)
	require.NotEmpty(t, events)

	hasDeletePhase := false
	for _, event := range events {
		if event.Phase == status.PhaseDeletingWorkspace {
			hasDeletePhase = true
		}
	}
	assert.True(t, hasDeletePhase, "expected a delete-phase journal event, got %+v", events)
}

func TestDeleteCmd_DeleteMultipleReusesResolvedClients(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	idA, idB := "chosen-a", "chosen-b"
	argA, argB := "arg-a", "arg-b"
	newFake := func(id string) *fakeWorkspaceClient {
		return &fakeWorkspaceClient{
			workspace: id,
			context:   testContext,
			provider:  testProvider,
			config:    &provider.Workspace{ID: id, Context: testContext},
		}
	}
	fakeA := newFake(idA)
	fakeB := newFake(idB)
	cmd := &DeleteCmd{
		GlobalFlags:   &flags.GlobalFlags{ResultFormat: formatPlain},
		DeleteOptions: client2.DeleteOptions{Force: true},
	}
	devsyConfig := &config.Config{
		DefaultContext: testContext,
		Contexts:       map[string]*config.ContextConfig{testContext: {}},
	}
	reporter, err := newWorkspaceStatusReporter(formatPlain, os.Stdout, false)
	require.NoError(t, err)

	targets := map[string]resolvedDeleteTarget{
		argA: {ID: idA, Client: fakeA},
		argB: {ID: idB, Client: fakeB},
	}
	captureStdout(t, func() {
		require.NoError(t, cmd.deleteMultiple(
			t.Context(),
			devsyConfig,
			reporter,
			targets,
			[]string{argA, argB},
		))
	})
	require.True(t, fakeA.deleted)
	require.True(t, fakeB.deleted)

	dir, err := workspacejournal.DefaultDir()
	require.NoError(t, err)
	for _, id := range []string{idA, idB} {
		events, err := workspacejournal.Read(dir, id, workspacejournal.DefaultLimit)
		require.NoError(t, err)
		assert.NotEmpty(t, events, "expected journaled events under %s", id)
	}
}

func TestResolveDeleteTargets_FallsBackToRawArgument(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	missingArg := "missing"
	devsyConfig := &config.Config{
		DefaultContext: testContext,
		Contexts:       map[string]*config.ContextConfig{testContext: {}},
	}
	targets := resolveDeleteTargets(
		t.Context(),
		devsyConfig,
		platform.OwnerFilter(""),
		[]string{missingArg},
	)
	target, ok := targets[missingArg]
	require.True(t, ok)
	assert.Equal(t, missingArg, target.ID)
	assert.Nil(t, target.Client)
}
