package workspace

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type providerDeprecationSuite struct {
	suite.Suite
}

const (
	testAgentPath  = config.BinaryName
	testFileSource = "/tmp/custom-microsandbox.yaml"
)

func TestProviderDeprecationSuite(t *testing.T) {
	suite.Run(t, new(providerDeprecationSuite))
}

func (s *providerDeprecationSuite) SetupTest() {
	setupTestPathManager(s.T())
}

func (s *providerDeprecationSuite) TestLoadAllProvidersMarksDeprecatedByDriver() {
	require := s.Require()
	const legacyName = "legacy-runtime"
	const externalName = "microsandbox"
	const customFileName = "custom-file-runtime"
	devsyConfig := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {
				Providers: map[string]*config.ProviderConfig{
					legacyName:     {},
					externalName:   {},
					customFileName: {},
				},
			},
		},
	}
	require.NoError(config.SaveConfig(devsyConfig))
	for name, source := range map[string]provider.ProviderSource{
		legacyName:     {Internal: true, Raw: provider.MicrosandboxDriver},
		customFileName: {File: testFileSource, Raw: testFileSource},
	} {
		s.saveProviderConfig(&provider.ProviderConfig{
			Name: name, Version: "v1.0.0",
			Agent: provider.ProviderAgentConfig{
				Path: testAgentPath, Driver: provider.MicrosandboxDriver,
			},
			Exec:   provider.ProviderCommands{Command: []string{config.BinaryName}},
			Source: source,
		})
	}
	s.saveProviderConfig(&provider.ProviderConfig{
		Name:    externalName,
		Version: "v0.1.8",
		Agent: provider.ProviderAgentConfig{
			Path:     testAgentPath,
			Driver:   provider.ExternalDriver,
			External: provider.ProviderExternalDriverConfig{Binary: "RUNTIME"},
			Binaries: map[string][]*provider.ProviderBinary{
				"RUNTIME": {{
					OS: "linux", Arch: "amd64", Path: "https://example.invalid/runtime",
					Checksum: "0000000000000000000000000000000000000000000000000000000000000000",
				}},
			},
		},
		Exec: provider.ProviderCommands{Command: []string{config.BinaryName}},
		Source: provider.ProviderSource{
			Github: "devsy-org/devsy-provider-microsandbox",
			Raw:    "devsy-provider-microsandbox@v0.1.8",
		},
	})

	providers, err := LoadAllProviders(devsyConfig)
	require.NoError(err)
	s.assertDeprecationMetadata(providers[legacyName], true)
	s.assertDeprecationMetadata(providers[customFileName], true)
	s.assertDeprecationMetadata(providers[externalName], false)
}

func (s *providerDeprecationSuite) saveProviderConfig(providerConfig *provider.ProviderConfig) {
	s.Require().NoError(provider.SaveProviderConfig(testDefaultContext, providerConfig))
}

func (s *providerDeprecationSuite) assertDeprecationMetadata(
	entry *ProviderWithOptions,
	deprecated bool,
) {
	s.Require().NotNil(entry)
	s.Equal(deprecated, entry.Deprecated)
	data, err := json.Marshal(entry)
	s.Require().NoError(err)
	if deprecated {
		s.Contains(string(data), `"deprecated":true`)
	} else {
		s.NotContains(string(data), `"deprecated"`)
	}
}
