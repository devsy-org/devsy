package up

import (
	"context"
	"fmt"
	"testing"

	secretspkg "github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	workspaceEnvLogLevel          = "LOG_LEVEL"
	workspaceEnvLiteralAssignment = "LOG_LEVEL=custom-value"
)

func TestComposeWorkspaceEnv_AttachmentFillsMissingTarget(t *testing.T) {
	base, err := indexWorkspaceEnv([]string{"LOCAL=value"})
	require.NoError(t, err)
	requests, err := filterWorkspaceEnvRequests(base, []envVarRequest{{
		ref: localRef("ATTACHED"), target: "ATTACHED", origin: envVarAttached,
	}})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	got, err := composeWorkspaceEnv(base, []resolvedEnvVar{{
		assignment: "ATTACHED=managed",
	}})
	require.NoError(t, err)
	assert.Equal(t, []string{"ATTACHED=managed", "LOCAL=value"}, got)
}

func TestComposeWorkspaceEnv_LiteralWorkspaceEnvShadowsAttachment(t *testing.T) {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{workspaceEnvLiteralAssignment}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("local", "local", fixedSource{
		values: map[string]string{workspaceEnvLogLevel: "stored-value"},
	}))

	err := cmd.applyEnvVars(t.Context(), testEnvConfig(workspaceEnvLogLevel), resolver)
	require.NoError(t, err)
	assert.Equal(t, []string{workspaceEnvLiteralAssignment}, cmd.WorkspaceEnv)
}

func TestComposeWorkspaceEnv_ShadowingDoesNotDependOnValueLexicalOrder(t *testing.T) {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{workspaceEnvLiteralAssignment}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("local", "local", fixedSource{
		values: map[string]string{workspaceEnvLogLevel: "stored-value"},
	}))

	require.NoError(t, cmd.applyEnvVars(t.Context(), testEnvConfig(workspaceEnvLogLevel), resolver))
	assert.Equal(t, workspaceEnvLiteralAssignment, cmd.WorkspaceEnv[0])
}

func TestComposeWorkspaceEnv_ShadowedAttachmentIsNotResolved(t *testing.T) {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{workspaceEnvLogLevel + "=local"}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("local", "local", unavailableEnvSource{}))

	err := cmd.applyEnvVars(t.Context(), testEnvConfig(workspaceEnvLogLevel), resolver)
	require.NoError(t, err)
	assert.Equal(t, []string{workspaceEnvLogLevel + "=local"}, cmd.WorkspaceEnv)
}

func TestComposeWorkspaceEnv_ExplicitManagedTargetConflictsWithLiteralTarget(t *testing.T) {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{"APP_MODE=local"}
	cmd.EnvVars = []string{"STORED_MODE=APP_MODE"}
	err := cmd.applyEnvVars(t.Context(), testEnvConfig(), secretspkg.NewResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_MODE")
	assert.Contains(t, err.Error(), "both --workspace-env and --env")
	assert.NotContains(t, err.Error(), "local")
	assert.Equal(t, []string{"APP_MODE=local"}, cmd.WorkspaceEnv)
}

func TestComposeWorkspaceEnv_DuplicateLiteralTargetRejected(t *testing.T) {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{"APP_MODE=a", "APP_MODE=b"}
	err := cmd.applyEnvVars(t.Context(), testEnvConfig(), secretspkg.NewResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"APP_MODE"`)
	assert.NotContains(t, err.Error(), "=a")
	assert.NotContains(t, err.Error(), "=b")
}

func TestComposeWorkspaceEnv_ValueMayContainEquals(t *testing.T) {
	assignment, err := parseWorkspaceEnvAssignment("TOKEN=one=two")
	require.NoError(t, err)
	assert.Equal(t, workspaceEnvAssignment{name: "TOKEN", value: "one=two"}, assignment)
}

func TestComposeWorkspaceEnv_OutputHasUniqueTargets(t *testing.T) {
	base, err := indexWorkspaceEnv([]string{"A=one"})
	require.NoError(t, err)
	got, err := composeWorkspaceEnv(base, []resolvedEnvVar{{
		assignment: "B=two",
	}})
	require.NoError(t, err)
	assert.Equal(t, []string{"A=one", "B=two"}, got)
	indexed, err := indexWorkspaceEnv(got)
	require.NoError(t, err)
	assert.Len(t, indexed, 2)
}

type unavailableEnvSource struct{}

func (unavailableEnvSource) Get(_ context.Context, name string) (secretspkg.ResolvedSecret, error) {
	return secretspkg.ResolvedSecret{}, fmt.Errorf("unexpected resolution of %s", name)
}
