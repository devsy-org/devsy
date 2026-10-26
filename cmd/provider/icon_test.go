package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/suite"
	"sigs.k8s.io/yaml"
)

const (
	iconProviderName = "icon-provider"
	iconLightPath    = "assets/light.svg"
	iconDarkPath     = "assets/dark.svg"
	iconLightSVG     = `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8">` +
		`<rect width="8" height="8" fill="#ff0000"/></svg>`
	iconDarkSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8">` +
		`<rect width="8" height="8" fill="#0000ff"/></svg>`
	iconResultFormatJSON = "json"
)

type providerIconCmdSuite struct {
	suite.Suite

	devsyConfig *config.Config
}

func TestProviderIconCmdSuite(t *testing.T) {
	suite.Run(t, new(providerIconCmdSuite))
}

func (s *providerIconCmdSuite) SetupTest() {
	s.T().Setenv("DEVSY_HOME", s.T().TempDir())
	s.devsyConfig = setupProviderOperationTest(s.T())
}

func (s *providerIconCmdSuite) TestPrintsCachedImagePerTheme() {
	added := s.installWithIcons()
	want := map[string]string{"light": iconLightSVG, "dark": iconDarkSVG}

	for theme, svg := range want {
		out, err := s.run(iconProviderName, theme)
		s.Require().NoError(err)

		var result providerpkg.ProviderIconResult
		s.Require().NoError(json.Unmarshal([]byte(out), &result))
		s.Require().Equal(added.IconRevision, result.Revision)
		s.Require().Equal("image/svg+xml", result.MimeType)
		s.Require().False(result.Missing)
		decoded, err := base64.StdEncoding.DecodeString(result.DataBase64)
		s.Require().NoError(err)
		s.Require().Equal(svg, string(decoded))
	}
}

func (s *providerIconCmdSuite) TestMissingVariantIsANormalResult() {
	s.install(
		iconProviderName,
		&providerpkg.ProviderConfig{Icon: iconLightPath, IconDark: iconDarkPath},
		map[string]string{iconLightPath: iconLightSVG},
	)

	out, err := s.run(iconProviderName, "dark")
	s.Require().NoError(err)

	var raw map[string]any
	s.Require().NoError(json.Unmarshal([]byte(out), &raw))
	s.Require().Equal(true, raw["missing"])
	s.Require().NotContains(raw, "dataBase64")
	s.Require().NotEmpty(raw["revision"])
}

func (s *providerIconCmdSuite) TestProviderWithoutIconsIsMissing() {
	s.install(iconProviderName, &providerpkg.ProviderConfig{}, nil)

	out, err := s.run(iconProviderName, "light")
	s.Require().NoError(err)

	var result providerpkg.ProviderIconResult
	s.Require().NoError(json.Unmarshal([]byte(out), &result))
	s.Require().True(result.Missing)
	s.Require().Empty(result.DataBase64)
}

func (s *providerIconCmdSuite) TestRejectsUnknownProvider() {
	_, err := s.run("does-not-exist", "light")

	s.Require().ErrorContains(err, "not found")
}

func (s *providerIconCmdSuite) TestRejectsInvalidTheme() {
	s.installWithIcons()

	_, err := s.run(iconProviderName, "sepia")

	s.Require().ErrorContains(err, "theme")
}

func (s *providerIconCmdSuite) TestRejectsInvalidProviderIdentityBeforeDiskRead() {
	for _, name := range []string{
		"../" + iconProviderName,
		"/tmp/provider",
		`..\provider`,
		"bad\x00name",
		strings.Repeat("a", 33),
		"",
	} {
		s.Run(name, func() {
			out, err := s.run(name, "light")
			s.Require().ErrorContains(err, "provider name")
			s.Require().Empty(out)
			s.Require().NotContains(err.Error(), "not found")
		})
	}
}

func (s *providerIconCmdSuite) TestRejectsUnsafeContextBeforeConfigRead() {
	configPath := s.T().TempDir()
	s.T().Setenv(config.EnvConfig, configPath)

	for _, contextName := range []string{
		"../../other",
		`..\target`,
		`C:\target`,
		strings.Repeat("a", 256),
	} {
		global := &flags.GlobalFlags{Context: contextName, ResultFormat: iconResultFormatJSON}
		command := NewIconCmd(global)
		err := command.RunE(command, []string{iconProviderName})
		s.ErrorContains(err, "invalid provider icon context")
	}
}

func (s *providerIconCmdSuite) TestAcceptsSafeUnicodeLegacyContext() {
	const legacyContext = "legacy-\u6771\u4eac"
	s.devsyConfig.DefaultContext = legacyContext
	s.devsyConfig.Contexts[legacyContext] = &config.ContextConfig{
		Providers: map[string]*config.ProviderConfig{},
	}
	s.installWithIcons()

	out, err := s.run(iconProviderName, "light")

	s.Require().NoError(err)
	s.NotEmpty(out)
}

func (s *providerIconCmdSuite) TestDoesNotInitializeProvider() {
	s.installWithIcons()

	_, err := s.run(iconProviderName, "light")
	s.Require().NoError(err)

	stored, err := config.LoadConfig(s.devsyConfig.DefaultContext, "")
	s.Require().NoError(err)
	state := stored.Current().Providers[iconProviderName]
	s.Require().NotNil(state)
	s.Require().False(state.Initialized)
	s.Require().False(state.InitAttempted)
}

func (s *providerIconCmdSuite) TestListKeepsIconMetadataWithoutImageBytes() {
	added := s.installWithIcons()
	reloaded, err := config.LoadConfig(s.devsyConfig.DefaultContext, "")
	s.Require().NoError(err)
	providers, err := workspace.LoadAllProviders(reloaded)
	s.Require().NoError(err)

	cmd := &ListCmd{}
	output := captureProviderListOutput(s.T(), func() error {
		return cmd.renderInstalledJSON(reloaded, reloaded.Current().Providers, providers)
	})
	s.Require().NoError(output.err)

	var result map[string]struct {
		Config map[string]any `json:"config"`
	}
	s.Require().NoError(json.Unmarshal([]byte(output.text), &result))
	entry := result[iconProviderName].Config
	s.Require().Equal(iconLightPath, entry["icon"])
	s.Require().Equal(iconDarkPath, entry["iconDark"])
	s.Require().Equal(added.IconRevision, entry["iconRevision"])
	s.Require().NotEmpty(entry["iconRevision"])
	s.Require().NotContains(output.text, "dataBase64")
	s.Require().NotContains(output.text, base64.StdEncoding.EncodeToString([]byte(iconLightSVG)))
}

func (s *providerIconCmdSuite) install(
	name string, manifest *providerpkg.ProviderConfig,
	assets map[string]string,
) *providerpkg.ProviderConfig {
	dir := s.T().TempDir()
	manifest.Name = name
	manifest.Version = "1.0.0"
	manifest.Exec = providerpkg.ProviderCommands{Command: []string{"true"}}
	raw, err := yaml.Marshal(manifest)
	s.Require().NoError(err)
	source := filepath.Join(dir, "provider.yaml")
	s.Require().NoError(os.WriteFile(source, raw, 0o600))
	for assetName, content := range assets {
		target := filepath.Join(dir, filepath.FromSlash(assetName))
		s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o700))
		s.Require().NoError(os.WriteFile(target, []byte(content), 0o600))
	}
	added, err := workspace.AddProvider(context.Background(), s.devsyConfig, name, source)
	s.Require().NoError(err)
	return added
}

func (s *providerIconCmdSuite) installWithIcons() *providerpkg.ProviderConfig {
	return s.install(
		iconProviderName,
		&providerpkg.ProviderConfig{Icon: iconLightPath, IconDark: iconDarkPath},
		map[string]string{iconLightPath: iconLightSVG, iconDarkPath: iconDarkSVG},
	)
}

func (s *providerIconCmdSuite) run(name, theme string) (string, error) {
	cmd := &IconCmd{
		GlobalFlags: &flags.GlobalFlags{ResultFormat: iconResultFormatJSON},
		Theme:       theme,
	}
	var out bytes.Buffer
	err := cmd.Run(s.devsyConfig, name, &out)
	return out.String(), err
}
