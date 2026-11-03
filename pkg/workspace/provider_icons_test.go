package workspace

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
	"sigs.k8s.io/yaml"
)

const (
	iconTestProvider        = "icon-provider"
	iconTestLight           = "assets/light.svg"
	iconTestDark            = "assets/dark.svg"
	iconThemeLight          = "light"
	iconThemeDark           = "dark"
	iconRed                 = "#ff0000"
	iconGreen               = "#00ff00"
	iconBlue                = "#0000ff"
	iconYellow              = "#ffff00"
	iconForgedRevison       = "forged"
	iconTestVersion         = "1.0.0"
	iconTestVersionTag      = "v" + iconTestVersion
	iconTestSourceRepo      = "owner/repo"
	iconTestRuntimeFilename = "runtime"
)

type providerIconSuite struct {
	suite.Suite

	devsyConfig *config.Config
}

func TestProviderIconSuite(t *testing.T) {
	suite.Run(t, new(providerIconSuite))
}

func (s *providerIconSuite) SetupTest() {
	setupTestPathManager(s.T())
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	config.ResetPathManager()
	initial := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
	}
	s.Require().NoError(config.SaveConfig(initial))
	s.devsyConfig = initial
}

func svgFill(color string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8">` +
		`<rect width="8" height="8" fill="` + color + `"/></svg>`
}

func (s *providerIconSuite) TestAddProviderWithoutBinariesSelectsIcons() {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})

	added := s.add(source)

	s.Require().NotEmpty(added.IconRevision)
	stored := s.installed(iconTestProvider)
	s.Require().Equal(added.IconRevision, stored.IconRevision)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
}

func (s *providerIconSuite) TestAddProviderWithMissingAssetStaysUsable() {
	source := s.writeSource(s.iconManifest(), map[string]string{iconTestLight: svgFill(iconRed)})

	s.add(source)

	stored := s.installed(iconTestProvider)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireMissing(s.readIcon(stored, iconThemeDark))
}

func (s *providerIconSuite) TestAuthorManifestCannotSupplyGeneratedFields() {
	forged := s.iconManifest()
	forged.IconRevision = iconForgedRevison
	forged.Source.ResolvedRef = "v9.9.9"
	forged.Source.ResolvedManifestURL = "https://example.invalid/provider.yaml"
	source := s.writeSource(forged, map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})

	s.add(source)

	stored := s.installed(iconTestProvider)
	s.Require().NotEqual(iconForgedRevison, stored.IconRevision)
	s.Require().NotEqual("v9.9.9", stored.Source.ResolvedRef)
	s.Require().NotEqual("https://example.invalid/provider.yaml", stored.Source.ResolvedManifestURL)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
}

func (s *providerIconSuite) TestIconOnlyUpdateSelectsNewRevision() {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	first := s.add(source)

	// Same manifest bytes and same icon paths: only the image bytes change.
	s.writeAssets(filepath.Dir(source), map[string]string{iconTestLight: svgFill(iconGreen)})
	second := s.update("")

	s.Require().NotEmpty(second.IconRevision)
	s.Require().NotEqual(first.IconRevision, second.IconRevision)
	stored := s.installed(iconTestProvider)
	s.Require().Equal(second.IconRevision, stored.IconRevision)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconGreen))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
	// The previous revision stays complete for a reader that still holds it.
	s.requireImage(s.readIcon(first, iconThemeLight), svgFill(iconRed))
}

func (s *providerIconSuite) TestUpdateWithUnchangedBinariesSelectsNewRevision() {
	manifest, runtimeRoot := s.runtimeManifest()
	source := s.writeSource(manifest, map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	first := s.add(source)
	binariesBefore, err := runtimeRoot.ReadFile(iconTestRuntimeFilename)
	s.Require().NoError(err)

	s.writeAssets(filepath.Dir(source), map[string]string{iconTestDark: svgFill(iconYellow)})
	second := s.update("")

	s.Require().NotEqual(first.IconRevision, second.IconRevision)
	s.requireImage(s.readIcon(second, iconThemeDark), svgFill(iconYellow))
	binariesAfter, err := runtimeRoot.ReadFile(iconTestRuntimeFilename)
	s.Require().NoError(err)
	s.Require().Equal(binariesBefore, binariesAfter)
}

func (s *providerIconSuite) TestSourceChangeNeverShowsPreviousSourceIcon() {
	firstSource := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	s.add(firstSource)

	withoutIcons := s.writeSource(&provider.ProviderConfig{Version: "2.0.0"}, nil)
	replaced := s.update(withoutIcons)

	stored := s.installed(iconTestProvider)
	s.Require().Equal(replaced.IconRevision, stored.IconRevision)
	s.requireMissing(s.readIcon(stored, iconThemeLight))
	s.requireMissing(s.readIcon(stored, iconThemeDark))

	withIcons := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconYellow),
		iconTestDark:  svgFill(iconGreen),
	})
	s.update(withIcons)

	stored = s.installed(iconTestProvider)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconYellow))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconGreen))
}

func (s *providerIconSuite) TestFailedReplacementKeepsPreviousRevision() {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	first := s.add(source)

	replacementSource := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconGreen),
		iconTestDark:  svgFill(iconYellow),
	})
	raw, resolved, err := provider.ResolveProvider(context.Background(), replacementSource)
	s.Require().NoError(err)
	params := ProviderParams{
		DevsyConfig:  s.reload(),
		ProviderName: iconTestProvider,
		Raw:          raw,
		Source:       resolved,
	}
	replacement, err := parseAndValidateProvider(params)
	s.Require().NoError(err)
	prepared, err := prepareProviderReplacement(context.Background(), params, replacement)
	s.Require().NoError(err)
	defer func() { s.Require().NoError(os.RemoveAll(prepared.stage)) }()
	s.Require().NotEqual(first.IconRevision, replacement.IconRevision)

	sentinel := errors.New("owned injected commit failure")
	err = prepared.commit(
		params,
		replacement,
		updateCommitFailureOps("manifest after save", sentinel),
	)

	s.Require().ErrorIs(err, sentinel)
	stored := s.installed(iconTestProvider)
	s.Require().Equal(first.IconRevision, stored.IconRevision)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
	// The staged revision is complete on its own even though it was not selected.
	s.requireImage(s.readIcon(replacement, iconThemeLight), svgFill(iconGreen))
}

func (s *providerIconSuite) TestCloneKeepsRevisionAfterOriginalAndSourceAreGone() {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	original := s.add(source)

	clone, err := CloneProvider(context.Background(), s.reload(), "icon-copy", iconTestProvider)
	s.Require().NoError(err)
	s.Require().Equal(original.IconRevision, clone.Config.IconRevision)

	originalCache, err := provider.GetProviderAssetsDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(err)
	cloneCache, err := provider.GetProviderAssetsDir(testDefaultContext, "icon-copy")
	s.Require().NoError(err)
	s.Require().NotEqual(originalCache, cloneCache)
	s.Require().DirExists(cloneCache)
	cloneDir, err := provider.GetProviderDir(testDefaultContext, "icon-copy")
	s.Require().NoError(err)
	s.Require().Equal(filepath.Join(cloneDir, provider.ProviderAssetsDirName), cloneCache)
	originalDir, err := provider.GetProviderDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(err)
	s.Require().NoError(os.RemoveAll(originalDir))
	s.Require().NoError(os.RemoveAll(filepath.Dir(source)))

	stored := s.installed("icon-copy")
	s.Require().Equal(original.IconRevision, stored.IconRevision)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
}

func (s *providerIconSuite) TestRenameKeepsIcon() {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	original := s.add(source)

	s.Require().NoError(provider.MoveProvider(s.reload(), iconTestProvider, "icon-renamed"))

	stored := s.installed("icon-renamed")
	s.Require().Equal(original.IconRevision, stored.IconRevision)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
}

func (s *providerIconSuite) TestDeletingCloneKeepsSharedRevisionForOriginal() {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	s.add(source)
	_, err := CloneProvider(context.Background(), s.reload(), "icon-copy", iconTestProvider)
	s.Require().NoError(err)

	cloneDir, err := provider.GetProviderDir(testDefaultContext, "icon-copy")
	s.Require().NoError(err)
	s.Require().NoError(os.RemoveAll(cloneDir))

	s.requireImage(s.readIcon(s.installed(iconTestProvider), iconThemeLight), svgFill(iconRed))
}

func (s *providerIconSuite) TestReadersAtEveryCommitBoundarySelectCompleteRevision() {
	manifest, _ := s.runtimeManifest()
	source := s.writeSource(manifest, map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	previous := s.add(source)
	s.writeAssets(filepath.Dir(source), map[string]string{
		iconTestLight: svgFill(iconGreen),
		iconTestDark:  svgFill(iconYellow),
	})
	raw, resolved, err := provider.ResolveProvider(context.Background(), source)
	s.Require().NoError(err)
	params := ProviderParams{
		DevsyConfig: s.reload(), ProviderName: iconTestProvider, Raw: raw, Source: resolved,
	}
	replacement, err := parseAndValidateProvider(params)
	s.Require().NoError(err)
	checkReader := func() {
		current := s.installed(iconTestProvider)
		if current.IconRevision == previous.IconRevision {
			s.requireImage(s.readIcon(current, iconThemeLight), svgFill(iconRed))
			s.requireImage(s.readIcon(current, iconThemeDark), svgFill(iconBlue))
		} else {
			s.Require().Equal(replacement.IconRevision, current.IconRevision)
			s.requireImage(s.readIcon(current, iconThemeLight), svgFill(iconGreen))
			s.requireImage(s.readIcon(current, iconThemeDark), svgFill(iconYellow))
		}
		// Readers holding the old manifest remain valid after pointer publication.
		s.requireImage(s.readIcon(previous, iconThemeLight), svgFill(iconRed))
	}
	ops := defaultProviderUpdateCommitOps()
	rename := ops.rename
	ops.rename = func(from, to string) error {
		checkReader()
		err := rename(from, to)
		checkReader()
		return err
	}
	saveManifest := ops.saveManifest
	ops.saveManifest = func(context string, cfg *provider.ProviderConfig) error {
		checkReader()
		err := saveManifest(context, cfg)
		checkReader()
		return err
	}
	updateConfig := ops.updateConfig
	ops.updateConfig = func(context, profile string, update func(*config.Config) error) error {
		checkReader()
		err := updateConfig(context, profile, update)
		checkReader()
		return err
	}
	s.Require().
		NoError(replaceProviderPreparedWithOps(context.Background(), params, replacement, ops))
	s.Require().Equal(replacement.IconRevision, s.installed(iconTestProvider).IconRevision)
	s.requireImage(s.readIcon(s.installed(iconTestProvider), iconThemeLight), svgFill(iconGreen))
}

// writeSource writes a local provider manifest next to its relative assets and
// returns the manifest path.
func (s *providerIconSuite) writeSource(
	manifest *provider.ProviderConfig,
	assets map[string]string,
) string {
	dir := s.T().TempDir()
	manifest.Name = iconTestProvider
	if manifest.Version == "" {
		manifest.Version = iconTestVersion
	}
	manifest.Exec = provider.ProviderCommands{Command: []string{updateNoopCommand}}
	raw, err := yaml.Marshal(manifest)
	s.Require().NoError(err)
	path := filepath.Join(dir, "provider.yaml")
	s.Require().NoError(os.WriteFile(path, raw, 0o600))
	s.writeAssets(dir, assets)
	return path
}

func (s *providerIconSuite) writeAssets(dir string, assets map[string]string) {
	for name, content := range assets {
		target := filepath.Join(dir, filepath.FromSlash(name))
		s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o700))
		s.Require().NoError(os.WriteFile(target, []byte(content), 0o600))
	}
}

func (s *providerIconSuite) iconManifest() *provider.ProviderConfig {
	return &provider.ProviderConfig{Icon: iconTestLight, IconDark: iconTestDark}
}

func (s *providerIconSuite) reload() *config.Config {
	loaded, err := config.LoadConfig(testDefaultContext, "")
	s.Require().NoError(err)
	return loaded
}

func (s *providerIconSuite) add(source string) *provider.ProviderConfig {
	added, err := AddProvider(context.Background(), s.reload(), iconTestProvider, source)
	s.Require().NoError(err)
	return added
}

func (s *providerIconSuite) update(source string) *provider.ProviderConfig {
	updated, err := UpdateProvider(context.Background(), s.reload(), iconTestProvider, source)
	s.Require().NoError(err)
	return updated
}

func (s *providerIconSuite) installed(name string) *provider.ProviderConfig {
	loaded, err := provider.LoadProviderConfig(testDefaultContext, name)
	s.Require().NoError(err)
	return loaded
}

func (s *providerIconSuite) readIcon(
	cfg *provider.ProviderConfig,
	theme string,
) *provider.ProviderIconResult {
	result, err := provider.ReadProviderIcon(testDefaultContext, cfg, theme)
	s.Require().NoError(err)
	return result
}

func (s *providerIconSuite) requireImage(result *provider.ProviderIconResult, want string) {
	s.Require().False(result.Missing)
	got, err := base64.StdEncoding.DecodeString(result.DataBase64)
	s.Require().NoError(err)
	s.Require().Equal(want, string(got))
}

func (s *providerIconSuite) requireMissing(result *provider.ProviderIconResult) {
	s.Require().True(result.Missing)
	s.Require().Empty(result.DataBase64)
}

func (s *providerIconSuite) runtimeManifest() (*provider.ProviderConfig, *os.Root) {
	dir := s.T().TempDir()
	content := []byte("external runtime")
	external := filepath.Join(dir, iconTestRuntimeFilename)
	s.Require().NoError(os.WriteFile(external, content, 0o600))
	root, err := os.OpenRoot(dir)
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.Require().NoError(root.Close()) })
	manifest := s.iconManifest()
	manifest.Binaries = map[string][]*provider.ProviderBinary{
		updateRuntimeKey: {updateTestBinary(external, content)},
	}
	return manifest, root
}

func TestSkipReasonSuite(t *testing.T) {
	suite.Run(t, new(skipReasonSuite))
}

type skipReasonSuite struct {
	suite.Suite
}

func (s *skipReasonSuite) TestResolvedProvenanceIsNotASourceChange() {
	original := provider.ProviderSource{
		Github:      iconTestSourceRepo,
		Raw:         iconTestSourceRepo,
		ResolvedRef: iconTestVersionTag,
	}
	current := original
	current.ResolvedRef = "v1.1.0"
	current.ResolvedManifestURL = "https://github.com/owner/repo/releases/download/v1.1.0/provider.yaml"

	s.Require().Empty(providerUpdateSkipReason(original, current, iconTestVersion, "2.0.0"))
}

func (s *skipReasonSuite) TestIdentityChangeIsASourceChange() {
	original := provider.ProviderSource{Github: iconTestSourceRepo, Raw: iconTestSourceRepo}
	current := provider.ProviderSource{Github: "other/repo", Raw: "other/repo"}

	s.Require().Equal(
		"provider source changed",
		providerUpdateSkipReason(original, current, iconTestVersion, "2.0.0"),
	)
}
