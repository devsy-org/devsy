package clientimplementation

import (
	"testing"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestDaemonGitHubPermissionRequiresOptInAndPreservesInstancePolicy(t *testing.T) {
	for _, optedIn := range []bool{false, true} {
		for _, gitDisabled := range []bool{false, true} {
			value := config.BoolFalse
			if optedIn {
				value = config.BoolTrue
			}
			cfg := &config.Config{
				DefaultContext: "default",
				Contexts: map[string]*config.ContextConfig{
					"default": {Options: map[string]config.OptionValue{
						config.ContextOptionGitHubCLIForwarding:        {Value: value},
						config.ContextOptionSSHInjectGitCredentials:    {Value: config.BoolTrue},
						config.ContextOptionSSHInjectDockerCredentials: {Value: config.BoolTrue},
					}},
				},
			}
			require.Equal(t, optedIn, getCredentialConfig(cfg, nil).github)
			workspace := &managementv1.DevsyWorkspaceInstance{}
			workspace.Status.Instance = &storagev1.DevsyWorkspaceTemplateDefinition{
				CredentialForwarding: &storagev1.CredentialForwarding{
					Git: &storagev1.GitCredentialForwarding{Disabled: gitDisabled},
				},
			}
			got := getCredentialConfig(cfg, workspace)
			require.Equal(t, optedIn && !gitDisabled, got.github)
			require.Equal(t, !gitDisabled, got.git)
			require.True(t, got.docker)
		}
	}
}
