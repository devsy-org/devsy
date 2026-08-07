package up

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
)

func TestBuildIDEParams_GPGAgentForwardingParity(t *testing.T) {
	tests := []struct {
		name          string
		cmdFlag       bool
		contextOption string
		expected      bool
	}{
		{
			name:          "enabled by context option only",
			cmdFlag:       false,
			contextOption: config.BoolTrue,
			expected:      true,
		},
		{
			name:          "enabled by cli flag only",
			cmdFlag:       true,
			contextOption: config.BoolFalse,
			expected:      true,
		},
		{
			name:          "disabled when both are off",
			cmdFlag:       false,
			contextOption: config.BoolFalse,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &UpCmd{
				GlobalFlags: &flags.GlobalFlags{},
				CLIOptions: provider2.CLIOptions{
					SSHAuthSockID:    "sock-id",
					GitSSHSigningKey: "ssh-sign-key",
				},
				GPGAgentForwarding: tt.cmdFlag,
				IDELaunch:          opener.LaunchAuto,
			}

			devsyConfig := testConfigWithGPGForwardingOption(tt.contextOption)
			wctx := &workspaceContext{
				user:       "vscode",
				result:     &config2.Result{},
				tunnelPort: 10800,
			}

			params := cmd.buildIDEParams(devsyConfig, nil, wctx)

			assert.Equal(t, tt.expected, params.GPGAgentForwarding)
			assert.Equal(t, "sock-id", params.SSHAuthSockID)
			assert.Equal(t, "ssh-sign-key", params.GitSSHSigningKey)
			assert.Equal(t, "vscode", params.User)
			assert.Equal(t, wctx.result, params.Result)
			assert.True(t, params.TunnelMode)
			assert.Equal(t, opener.LaunchAuto, params.Launch)
		})
	}
}

func testConfigWithGPGForwardingOption(value string) *config.Config {
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {
				Options: map[string]config.OptionValue{
					config.ContextOptionGPGAgentForwarding: {Value: value},
				},
			},
		},
	}
}
