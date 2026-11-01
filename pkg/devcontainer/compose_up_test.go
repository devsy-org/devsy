package devcontainer

import (
	"testing"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	composeUpTestWorkspaceID  = "workspace-name"
	composeUpTestWorkspaceUID = "workspace-uid"
	composeUpTestUserKey      = "FOO"
	composeUpTestUserValue    = "bar"
)

func generateComposeTestEnvironment(
	t *testing.T,
	workspaceConfig *provider.AgentWorkspaceInfo,
	containerEnv map[string]string,
) composetypes.MappingWithEquals {
	t.Helper()
	workspaceConfig.CLIOptions.GPUAvailability = stringFalse
	r := &runner{workspaceConfig: workspaceConfig}
	mergedConfig := &config.MergedDevContainerConfig{}
	mergedConfig.ContainerEnv = containerEnv
	project := r.generateDockerComposeUpProject(&composeUpParams{
		parsedConfig: &config.SubstitutedConfig{Config: &config.DevContainerConfig{}},
		composeService: &composetypes.ServiceConfig{
			Name: composeSecretTestServiceName,
		},
		imageDetails: &config.ImageDetails{},
		mergedConfig: mergedConfig,
	})
	return project.Services[composeSecretTestServiceName].Environment
}

func testWorkspaceInfo() *provider.AgentWorkspaceInfo {
	return &provider.AgentWorkspaceInfo{
		Workspace: &provider.Workspace{
			ID:  composeUpTestWorkspaceID,
			UID: composeUpTestWorkspaceUID,
		},
	}
}

func userContainerEnv() map[string]string {
	return map[string]string{composeUpTestUserKey: composeUpTestUserValue}
}

func requireEnvValue(t *testing.T, env composetypes.MappingWithEquals, key, want string) {
	t.Helper()
	got, ok := env[key]
	require.True(t, ok, "%s missing from environment", key)
	require.NotNil(t, got)
	require.Equal(t, want, *got)
}

func TestGenerateDockerComposeUpProjectInjectsWorkspaceIdentity(t *testing.T) {
	env := generateComposeTestEnvironment(t, testWorkspaceInfo(), nil)
	requireEnvValue(t, env, pkgconfig.EnvWorkspaceID, composeUpTestWorkspaceID)
	requireEnvValue(t, env, pkgconfig.EnvWorkspaceUID, composeUpTestWorkspaceUID)
}

func TestGenerateDockerComposeUpProjectPreservesUserContainerEnv(t *testing.T) {
	env := generateComposeTestEnvironment(t, testWorkspaceInfo(), userContainerEnv())
	requireEnvValue(t, env, composeUpTestUserKey, composeUpTestUserValue)
	requireEnvValue(t, env, pkgconfig.EnvWorkspaceID, composeUpTestWorkspaceID)
}

func TestGenerateDockerComposeUpProjectWorkspaceIdentityWinsOverUserEnv(t *testing.T) {
	env := generateComposeTestEnvironment(t, testWorkspaceInfo(), map[string]string{
		pkgconfig.EnvWorkspaceID:  "user-id",
		pkgconfig.EnvWorkspaceUID: "user-uid",
	})
	requireEnvValue(t, env, pkgconfig.EnvWorkspaceID, composeUpTestWorkspaceID)
	requireEnvValue(t, env, pkgconfig.EnvWorkspaceUID, composeUpTestWorkspaceUID)
}

func TestGenerateDockerComposeUpProjectDoesNotMutateMergedContainerEnv(t *testing.T) {
	containerEnv := userContainerEnv()
	generateComposeTestEnvironment(t, testWorkspaceInfo(), containerEnv)
	require.Equal(t, userContainerEnv(), containerEnv)
}

func TestGenerateDockerComposeUpProjectWithoutWorkspaceConfig(t *testing.T) {
	for name, workspaceConfig := range map[string]*provider.AgentWorkspaceInfo{
		"no workspace":  {},
		"empty details": {Workspace: &provider.Workspace{}},
	} {
		t.Run(name, func(t *testing.T) {
			env := generateComposeTestEnvironment(t, workspaceConfig, userContainerEnv())
			requireEnvValue(t, env, composeUpTestUserKey, composeUpTestUserValue)
			require.NotContains(t, env, pkgconfig.EnvWorkspaceID)
			require.NotContains(t, env, pkgconfig.EnvWorkspaceUID)
		})
	}
}
