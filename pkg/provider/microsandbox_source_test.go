package provider

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type microsandboxSourceSuite struct {
	suite.Suite
}

func TestMicrosandboxSource(t *testing.T) {
	suite.Run(t, new(microsandboxSourceSuite))
}

func (s *microsandboxSourceSuite) TestLegacySelectorsKeepCanonicalIdentity() {
	for _, input := range []string{MicrosandboxDriver, DeprecatedMicrosandboxSource} {
		s.Run(input, func() {
			raw, source, err := ResolveProvider(context.Background(), input)
			s.Require().NoError(err)
			s.Equal(ProviderSource{Internal: true, Raw: MicrosandboxDriver}, *source)
			parsed, err := ParseProvider(bytes.NewReader(raw))
			s.Require().NoError(err)
			s.Equal(MicrosandboxDriver, parsed.Agent.Driver)
			s.Equal(MicrosandboxDriver, GetProviderSource(*source, "custom-name"))
		})
	}
}

func (s *microsandboxSourceSuite) TestLoadingLegacyRecordDoesNotMigrateOrRewriteIt() {
	setupTestHome(s.T())
	stored := &ProviderConfig{
		Name: "my-sandbox", Version: staleVersion,
		Source: ProviderSource{Internal: true, Raw: MicrosandboxDriver},
		Agent: ProviderAgentConfig{
			Driver: MicrosandboxDriver,
			Microsandbox: ProviderMicrosandboxDriverConfig{
				Memory: "1024", WorkspaceHostPermissions: "private",
			},
		},
		Options: map[string]*types.Option{
			"MICROSANDBOX_MEMORY": {Default: "1024"},
		},
		Exec: ProviderCommands{Command: []string{staleHelperShCommand}},
	}
	s.Require().NoError(SaveProviderConfig(config.DefaultContext, stored))
	dir, err := GetProviderDir(config.DefaultContext, stored.Name)
	s.Require().NoError(err)
	path := filepath.Join(dir, ProviderConfigFile)
	before, err := os.ReadFile(filepath.Clean(path))
	s.Require().NoError(err)

	loaded, err := LoadProviderConfig(config.DefaultContext, stored.Name)
	s.Require().NoError(err)
	s.Equal(stored.Name, loaded.Name)
	s.Equal(stored.Version, loaded.Version)
	s.Equal(stored.Source, loaded.Source)
	s.Equal(stored.Agent.Driver, loaded.Agent.Driver)
	s.Equal(stored.Agent.Microsandbox, loaded.Agent.Microsandbox)
	s.Equal(stored.Options, loaded.Options)
	s.Require().Len(loaded.Exec.Command, 1)
	s.NotContains(loaded.Exec.Command[0], "helper sh")
	after, err := os.ReadFile(filepath.Clean(path))
	s.Require().NoError(err)
	s.Equal(before, after)
}
