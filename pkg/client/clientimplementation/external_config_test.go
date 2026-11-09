package clientimplementation

import (
	"runtime"
	"testing"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type externalHandoffSuite struct{ suite.Suite }

func TestResolvedExternalConfigurationHandoff(
	t *testing.T,
) {
	suite.Run(t, new(externalHandoffSuite))
}

func (s *externalHandoffSuite) TestResolution() {
	for _, driver := range []string{provider.DockerDriver, provider.ExternalDriver} {
		s.Run(driver, func() {
			client := &workspaceClient{
				providerConfig: &provider.ProviderConfig{
					Name:  "external-handoff",
					Agent: provider.ProviderAgentConfig{Driver: "${DRIVER}"},
				},
				devsyConfig: &config.Config{
					DefaultContext: config.DefaultContext,
					Contexts: map[string]*config.ContextConfig{
						config.DefaultContext: {Providers: map[string]*config.ProviderConfig{
							"external-handoff": {
								Options: map[string]config.OptionValue{"DRIVER": {Value: driver}},
							},
						}},
					},
				},
			}
			compressed, info, err := client.AgentInfo(provider.CLIOptions{})
			if driver == provider.DockerDriver {
				s.Require().NoError(err)
				s.Require().NotEmpty(compressed)
				s.Require().Equal(driver, info.Agent.Driver)
				return
			}
			s.Require().ErrorContains(err, "agent.external.binary")
			s.Require().Empty(compressed)
			s.Require().Nil(info)
			client.providerConfig.Agent.External.Binary = "RUNTIME"
			client.providerConfig.Agent.Binaries = map[string][]*provider.ProviderBinary{
				"RUNTIME": {{OS: "linux", Arch: "amd64", Path: "https://example.invalid/runtime"}},
			}
			_, _, err = client.AgentInfo(provider.CLIOptions{})
			s.Require().ErrorContains(err, "SHA-256")
			client.providerConfig.Agent.Binaries["RUNTIME"][0].Checksum = hash.String("runtime")
			compressed, info, err = client.AgentInfo(provider.CLIOptions{})
			s.Require().NoError(err)
			s.Require().NotEmpty(compressed)
			s.Require().Equal(driver, info.Agent.Driver)
			s.Require().Equal(provider.DockerDriver, info.Agent.External.ImageBackend)
		})
	}
}

func (s *externalHandoffSuite) TestProviderOptionsStayOnHost() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	for _, tc := range providerOptionsCases() {
		s.Run(tc.name, func() {
			client := newProviderOptionsClient(tc.driver, tc.local)
			compressed, host, err := client.AgentInfo(tc.cli)
			s.Require().NoError(err)
			payload, _, err := agent.DecodeWorkspaceInfo(compressed)
			s.Require().NoError(err)
			for _, info := range []struct {
				value   *provider.AgentWorkspaceInfo
				options bool
			}{{host, tc.hostOptions}, {payload, tc.payloadOptions}} {
				s.Equal(info.options, len(info.value.Options) > 0)
				s.Equal(info.options, len(info.value.Workspace.Provider.Options) > 0)
				s.Equal(info.options, len(info.value.Machine.Provider.Options) > 0)
			}
			s.Equal(handoffOption("global"), client.devsyConfig.ProviderOptions(handoffProvider))
			s.Equal(handoffOption("workspace"), client.providerWorkspace.Provider.Options)
			s.Equal(handoffOption("machine"), client.providerMachine.Provider.Options)
		})
	}
}

type providerOptionsCase struct {
	name           string
	driver         string
	local          types.StrBool
	cli            provider.CLIOptions
	hostOptions    bool
	payloadOptions bool
}

func providerOptionsCases() []providerOptionsCase {
	return []providerOptionsCase{
		{
			name:        "external local disabled",
			driver:      provider.ExternalDriver,
			local:       config.BoolTrue,
			cli:         provider.CLIOptions{DisableDaemon: true},
			hostOptions: true,
		},
		{
			name: "external remote disabled", driver: provider.ExternalDriver,
			cli: provider.CLIOptions{DisableDaemon: true},
		},
		{
			name: "docker disabled", driver: provider.DockerDriver, local: config.BoolTrue,
			cli: provider.CLIOptions{DisableDaemon: true},
		},
		{
			name: "custom disabled", driver: provider.CustomDriver, local: config.BoolTrue,
			cli: provider.CLIOptions{DisableDaemon: true}, hostOptions: true, payloadOptions: true,
		},
		{
			name: "external daemon", driver: provider.ExternalDriver, local: config.BoolTrue,
			hostOptions: true, payloadOptions: true,
		},
		{
			name:   "external local platform",
			driver: provider.ExternalDriver,
			local:  config.BoolTrue,
			cli: provider.CLIOptions{
				Platform: devsy.PlatformOptions{Enabled: true},
			},
			hostOptions: true,
		},
	}
}

const handoffProvider = "handoff"

func handoffOption(value string) map[string]config.OptionValue {
	return map[string]config.OptionValue{"RUNTIME_OPTION": {Value: value}}
}

func newProviderOptionsClient(driver string, local types.StrBool) *workspaceClient {
	providerConfig := &provider.ProviderConfig{Name: handoffProvider}
	providerConfig.Agent.Driver = driver
	providerConfig.Agent.Local = local
	providerConfig.Agent.External.Binary = "HANDOFF_RUNTIME"
	providerConfig.Agent.Binaries = map[string][]*provider.ProviderBinary{
		"HANDOFF_RUNTIME": {
			{
				OS:       runtime.GOOS,
				Arch:     runtime.GOARCH,
				Path:     "https://example.invalid/runtime",
				Checksum: hash.String("runtime"),
			},
		},
	}
	return &workspaceClient{
		providerConfig: providerConfig,
		devsyConfig: &config.Config{
			DefaultContext: config.DefaultContext,
			Contexts: map[string]*config.ContextConfig{
				config.DefaultContext: {
					Providers: map[string]*config.ProviderConfig{
						handoffProvider: {Options: handoffOption("global")},
					},
				},
			},
		},
		providerWorkspace: &provider.Workspace{
			ID:      "workspace",
			Context: config.DefaultContext,
			Provider: provider.WorkspaceProviderConfig{
				Name:    handoffProvider,
				Options: handoffOption("workspace"),
			},
		},
		providerMachine: &provider.Machine{
			ID: "machine",
			Provider: provider.MachineProviderConfig{
				Name:    handoffProvider,
				Options: handoffOption("machine"),
			},
		},
	}
}
