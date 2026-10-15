package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type providerDeprecationSuite struct {
	suite.Suite
}

type templatedDriverCase struct {
	name          string
	savedDriver   string
	hasSavedValue bool
	defaultDriver string
	deprecated    bool
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

func (s *providerDeprecationSuite) TestTemplatedDriverUsesSavedOptionWithoutMutationOrCommand() {
	require := s.Require()
	commandMarker := filepath.Join(s.T().TempDir(), "option-command-ran")
	testCases := []templatedDriverCase{
		{
			name: "templated-microsandbox", savedDriver: provider.MicrosandboxDriver,
			hasSavedValue: true, defaultDriver: provider.DockerDriver, deprecated: true,
		},
		{
			name: "templated-docker", savedDriver: provider.DockerDriver,
			hasSavedValue: true, defaultDriver: provider.MicrosandboxDriver,
		},
		{name: "templated-unresolved"},
	}
	devsyConfig := s.saveTemplatedDriverFixtures(testCases, commandMarker)
	originalState, err := json.Marshal(devsyConfig.Current().Providers)
	require.NoError(err)

	providers, err := LoadAllProviders(devsyConfig)
	require.NoError(err)
	s.assertTemplatedDriverResults(providers, testCases)
	require.Equal("templated-microsandbox", devsyConfig.Current().DefaultProvider)
	afterState, err := json.Marshal(devsyConfig.Current().Providers)
	require.NoError(err)
	require.JSONEq(string(originalState), string(afterState))
	_, err = os.Stat(commandMarker)
	require.ErrorIs(err, os.ErrNotExist)
}

func (s *providerDeprecationSuite) saveTemplatedDriverFixtures(
	testCases []templatedDriverCase,
	commandMarker string,
) *config.Config {
	configuredProviders := make(map[string]*config.ProviderConfig, len(testCases))
	for _, tc := range testCases {
		state := &config.ProviderConfig{Initialized: true, InitAttempted: true}
		if tc.hasSavedValue {
			state.Options = map[string]config.OptionValue{
				"DRIVER": {Value: tc.savedDriver, UserProvided: true},
			}
		}
		configuredProviders[tc.name] = state
	}
	devsyConfig := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {
				DefaultProvider: "templated-microsandbox",
				Providers:       configuredProviders,
			},
		},
	}
	s.Require().NoError(config.SaveConfig(devsyConfig))
	for _, tc := range testCases {
		definitions := map[string]*types.Option{
			"UNUSED": {
				Command: "echo executed > " + shellescape.Quote(filepath.ToSlash(commandMarker)),
			},
		}
		if tc.defaultDriver != "" {
			definitions["DRIVER"] = &types.Option{Default: tc.defaultDriver}
		}
		s.saveProviderConfig(&provider.ProviderConfig{
			Name:    tc.name,
			Version: "v1.0.0",
			Options: definitions,
			Agent:   provider.ProviderAgentConfig{Path: testAgentPath, Driver: "${DRIVER}"},
			Exec:    provider.ProviderCommands{Command: []string{config.BinaryName}},
			Source: provider.ProviderSource{
				File: testFileSource,
				Raw:  testFileSource,
			},
		})
	}
	return devsyConfig
}

func (s *providerDeprecationSuite) assertTemplatedDriverResults(
	providers map[string]*ProviderWithOptions,
	testCases []templatedDriverCase,
) {
	require := s.Require()
	for _, tc := range testCases {
		entry := providers[tc.name]
		require.NotNil(entry)
		require.Equal(tc.deprecated, entry.Deprecated, tc.name)
		require.Equal("${DRIVER}", entry.Config.Agent.Driver)
		if tc.hasSavedValue {
			require.Equal(tc.savedDriver, entry.State.Options["DRIVER"].Value)
			require.True(entry.State.Options["DRIVER"].UserProvided)
			require.Nil(entry.State.Options["DRIVER"].Filled)
		} else {
			require.Empty(entry.State.Options)
		}
	}
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
