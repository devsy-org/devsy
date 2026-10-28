package workspace

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
)

const iconRegressionCloneName = "icon-copy"

func (s *providerIconSuite) TestAddCanceledDuringIconFetchDoesNotPublishProvider() {
	beforeConfig := s.iconRegressionConfigBytes()
	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()
	fixture := s.blockedIconSource(cancel)
	defer fixture.release()

	added, err := AddProvider(ctx, s.reload(), iconTestProvider, fixture.source)
	fixture.requireFinished(s)

	s.ErrorIs(err, context.Canceled)
	s.Nil(added)
	dir, pathErr := provider.GetProviderDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(pathErr)
	_, dirErr := os.Stat(dir)
	s.ErrorIs(dirErr, os.ErrNotExist)
	s.NotContains(s.reload().Current().Providers, iconTestProvider)
	s.Equal(beforeConfig, s.iconRegressionConfigBytes())
	retrySource := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	retried := s.add(retrySource)
	s.Require().NotNil(retried)
	stored := s.installed(iconTestProvider)
	s.Equal(retried, stored)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
}

func (s *providerIconSuite) TestUpdateCanceledDuringIconFetchPreservesInstalledProvider() {
	_, original := s.iconRegressionInstalledProvider()
	beforeManifest := s.iconRegressionManifestBytes()
	beforeConfig := s.iconRegressionConfigBytes()
	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()
	fixture := s.blockedIconSource(cancel)
	defer fixture.release()

	updated, err := UpdateProvider(ctx, s.reload(), iconTestProvider, fixture.source)
	fixture.requireFinished(s)

	s.ErrorIs(err, context.Canceled)
	s.Nil(updated)
	s.Equal(beforeManifest, s.iconRegressionManifestBytes())
	s.Equal(beforeConfig, s.iconRegressionConfigBytes())
	stored := s.installed(iconTestProvider)
	s.Equal(original, stored)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
}

func (s *providerIconSuite) TestAddIconChildTimeoutKeepsProviderUsable() {
	// The icon fetcher's own deadline expires while the provider caller stays live.
	ctx, cancel := context.WithTimeout(s.T().Context(), updateTestHangGuard)
	defer cancel()
	fixture := s.blockedIconSource(nil)
	defer fixture.release()

	added, err := AddProvider(ctx, s.reload(), iconTestProvider, fixture.source)
	fixture.requireFinished(s)

	s.Require().NoError(ctx.Err(), "only the optional icon request may time out")
	s.Require().NoError(err)
	s.Require().NotNil(added)
	stored := s.installed(iconTestProvider)
	s.Equal(added, stored)
	s.Require().NotNil(s.reload().Current().Providers[iconTestProvider])
	s.requireMissing(s.readIcon(stored, iconThemeLight))
	s.requireMissing(s.readIcon(stored, iconThemeDark))
}

func (s *providerIconSuite) TestCloneWithMissingIconCacheKeepsProviderUsable() {
	s.cloneWithUnavailableIconCache(false)
}

func (s *providerIconSuite) TestCloneWithCorruptIconCacheKeepsProviderUsable() {
	s.cloneWithUnavailableIconCache(true)
}

func (s *providerIconSuite) TestCloneCanceledWithMissingIconCacheDoesNotPublishProvider() {
	_, original := s.iconRegressionInstalledProvider()
	s.makeIconCacheUnavailable(original, false)
	sourceDir, sourceErr := provider.GetProviderDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(sourceErr)
	beforeSourceDir := s.iconRegressionTree(sourceDir)
	beforeManifest := s.iconRegressionManifestBytes()
	beforeConfig := s.iconRegressionConfigBytes()
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()

	clone, err := CloneProvider(ctx, s.reload(), iconRegressionCloneName, iconTestProvider)

	s.ErrorIs(err, context.Canceled)
	s.Nil(clone)
	cloneDir, pathErr := provider.GetProviderDir(testDefaultContext, iconRegressionCloneName)
	s.Require().NoError(pathErr)
	_, cloneErr := os.Stat(cloneDir)
	s.ErrorIs(cloneErr, os.ErrNotExist)
	s.Equal(beforeManifest, s.iconRegressionManifestBytes())
	s.Equal(beforeConfig, s.iconRegressionConfigBytes())
	s.Equal(beforeSourceDir, s.iconRegressionTree(sourceDir))

	retried, retryErr := CloneProvider(
		s.T().Context(),
		s.reload(),
		iconRegressionCloneName,
		iconTestProvider,
	)
	s.Require().NoError(retryErr)
	s.Require().NotNil(retried)
	s.Equal(retried.Config, s.installed(iconRegressionCloneName))
}

// cancelOnceStagedContext reports cancellation only after marker exists. It
// lets a test cancel at a known point inside icon staging, after files are
// written but before the operation publishes the provider.
type cancelOnceStagedContext struct {
	context.Context
	marker string
}

func (c cancelOnceStagedContext) Err() error {
	if _, err := os.Stat(c.marker); err == nil {
		return context.Canceled
	}
	return c.Context.Err()
}

func (s *providerIconSuite) TestAddCanceledAfterIconStagingRemovesProviderDirAndAllowsRetry() {
	dir, err := provider.GetProviderDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(err)
	assetsDir, err := provider.GetProviderAssetsDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(err)
	beforeConfig := s.iconRegressionConfigBytes()
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	// storeIconBlobs creates revisions after both blobs are written and before
	// the revision metadata; the next ctx.Err check then reports cancellation.
	ctx := cancelOnceStagedContext{
		Context: s.T().Context(),
		marker:  filepath.Join(assetsDir, "revisions"),
	}

	added, err := AddProvider(ctx, s.reload(), iconTestProvider, source)
	s.ErrorIs(err, context.Canceled)
	s.Nil(added)
	_, statErr := os.Stat(dir)
	s.ErrorIs(statErr, os.ErrNotExist)
	s.Equal(beforeConfig, s.iconRegressionConfigBytes())

	retried := s.add(source)
	s.Require().NotNil(retried)
	stored := s.installed(iconTestProvider)
	s.Equal(retried, stored)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
}

func (s *providerIconSuite) TestCloneCanceledAfterIconCopyRemovesCloneDirAndAllowsRetry() {
	_, original := s.iconRegressionInstalledProvider()
	sourceDir, err := provider.GetProviderDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(err)
	cloneDir, err := provider.GetProviderDir(testDefaultContext, iconRegressionCloneName)
	s.Require().NoError(err)
	cloneAssets, err := provider.GetProviderAssetsDir(testDefaultContext, iconRegressionCloneName)
	s.Require().NoError(err)
	beforeConfig := s.iconRegressionConfigBytes()
	beforeSourceDir := s.iconRegressionTree(sourceDir)
	// copyIconVariant creates blobs after copying the light variant; the dark
	// variant's ctx.Err check then reports cancellation mid-copy.
	ctx := cancelOnceStagedContext{
		Context: s.T().Context(),
		marker:  filepath.Join(cloneAssets, "blobs"),
	}

	clone, err := CloneProvider(ctx, s.reload(), iconRegressionCloneName, iconTestProvider)
	s.ErrorIs(err, context.Canceled)
	s.Nil(clone)
	_, statErr := os.Stat(cloneDir)
	s.ErrorIs(statErr, os.ErrNotExist)
	s.Equal(beforeConfig, s.iconRegressionConfigBytes())
	s.Equal(beforeSourceDir, s.iconRegressionTree(sourceDir))

	retried, err := CloneProvider(
		s.T().Context(),
		s.reload(),
		iconRegressionCloneName,
		iconTestProvider,
	)
	s.Require().NoError(err)
	s.Require().NotNil(retried)
	stored := s.installed(iconRegressionCloneName)
	s.Equal(retried.Config, stored)
	s.Equal(original.IconRevision, stored.IconRevision)
	s.requireImage(s.readIcon(stored, iconThemeLight), svgFill(iconRed))
	s.requireImage(s.readIcon(stored, iconThemeDark), svgFill(iconBlue))
}

func (s *providerIconSuite) TestCloneWithMissingIconCacheStillRejectsUnavailableBinary() {
	_, original := s.iconRegressionInstalledProvider()
	// An installed provider may only offer binaries for a different platform.
	otherOS := updateWindowsOS
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	binary := updateTestBinary("https://example.invalid/runtime", []byte("runtime"))
	binary.OS = otherOS
	original.Binaries = map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}
	s.Require().NoError(provider.SaveProviderConfig(testDefaultContext, original))
	s.makeIconCacheUnavailable(original, false)
	beforeManifest := s.iconRegressionManifestBytes()
	beforeConfig := s.iconRegressionConfigBytes()

	clone, err := CloneProvider(
		s.T().Context(),
		s.reload(),
		iconRegressionCloneName,
		iconTestProvider,
	)

	s.Error(err)
	s.Nil(clone)
	dir, pathErr := provider.GetProviderDir(testDefaultContext, iconRegressionCloneName)
	s.Require().NoError(pathErr)
	_, manifestErr := os.Stat(filepath.Join(dir, provider.ProviderConfigFile))
	s.ErrorIs(manifestErr, os.ErrNotExist)
	s.Equal(beforeManifest, s.iconRegressionManifestBytes())
	s.Equal(beforeConfig, s.iconRegressionConfigBytes())
}

func (s *providerIconSuite) cloneWithUnavailableIconCache(corrupt bool) {
	source, original := s.iconRegressionInstalledProvider()
	s.makeIconCacheUnavailable(original, corrupt)
	// Cloning uses the installed provider even when its author source is gone.
	s.Require().NoError(os.RemoveAll(filepath.Dir(source)))
	beforeManifest := s.iconRegressionManifestBytes()
	before := s.reload()

	clone, err := CloneProvider(s.T().Context(), before, iconRegressionCloneName, iconTestProvider)

	s.Require().NoError(err)
	s.Require().NotNil(clone)
	want := *original
	want.Name = iconRegressionCloneName
	want.IconRevision = ""
	stored := s.installed(iconRegressionCloneName)
	s.Equal(&want, clone.Config)
	s.Equal(&want, stored)
	s.requireMissing(s.readIcon(stored, iconThemeLight))
	s.requireMissing(s.readIcon(stored, iconThemeDark))
	after := s.reload()
	s.Require().NotNil(after.Current().Providers[iconRegressionCloneName])
	s.False(after.Current().Providers[iconRegressionCloneName].Initialized)
	s.Equal(before.Current().DefaultProvider, after.Current().DefaultProvider)
	s.Equal(
		before.Current().Providers[iconTestProvider],
		after.Current().Providers[iconTestProvider],
	)
	s.Equal(beforeManifest, s.iconRegressionManifestBytes())
}

func (s *providerIconSuite) iconRegressionInstalledProvider() (string, *provider.ProviderConfig) {
	source := s.writeSource(s.iconManifest(), map[string]string{
		iconTestLight: svgFill(iconRed),
		iconTestDark:  svgFill(iconBlue),
	})
	s.add(source)
	loaded := s.reload()
	loaded.Current().DefaultProvider = iconTestProvider
	state := loaded.Current().Providers[iconTestProvider]
	s.Require().NotNil(state)
	state.Initialized = true
	state.InitAttempted = true
	state.Options = map[string]config.OptionValue{
		updateKeptOption: {Value: "preserve original option", UserProvided: true},
	}
	s.Require().NoError(config.SaveConfig(loaded))
	original := s.installed(iconTestProvider)
	s.requireImage(s.readIcon(original, iconThemeLight), svgFill(iconRed))
	return source, original
}

func (s *providerIconSuite) makeIconCacheUnavailable(
	original *provider.ProviderConfig,
	corrupt bool,
) {
	cache, err := provider.GetProviderAssetsDir(testDefaultContext, original.Name)
	s.Require().NoError(err)
	if !corrupt {
		s.Require().NoError(os.RemoveAll(cache))
		return
	}
	// Keep valid revision metadata but corrupt the actual selected image blob.
	blob := filepath.Join(cache, "blobs", updateTestDigest([]byte(svgFill(iconRed))))
	s.Require().FileExists(blob)
	s.Require().NoError(os.WriteFile(blob, []byte("corrupt image"), 0o600))
}

func (s *providerIconSuite) iconRegressionConfigBytes() []byte {
	path, err := config.GetConfigPath()
	s.Require().NoError(err)
	return s.iconRegressionReadFile(filepath.Dir(path), filepath.Base(path))
}

func (s *providerIconSuite) iconRegressionManifestBytes() []byte {
	dir, err := provider.GetProviderDir(testDefaultContext, iconTestProvider)
	s.Require().NoError(err)
	return s.iconRegressionReadFile(dir, provider.ProviderConfigFile)
}

func (s *providerIconSuite) iconRegressionReadFile(directory, name string) []byte {
	root, err := os.OpenRoot(directory)
	s.Require().NoError(err)
	raw, readErr := root.ReadFile(name)
	closeErr := root.Close()
	s.Require().NoError(readErr)
	s.Require().NoError(closeErr)
	return raw
}

func (s *providerIconSuite) iconRegressionTree(directory string) map[string]string {
	root, err := os.OpenRoot(directory)
	s.Require().NoError(err)
	files := map[string]string{}
	walk := func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		raw, readErr := root.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		files[name] = string(raw)
		return nil
	}
	treeErr := fs.WalkDir(root.FS(), ".", walk)
	closeErr := root.Close()
	s.Require().NoError(treeErr)
	s.Require().NoError(closeErr)
	return files
}

type blockedProviderIconFixture struct {
	source              string
	requested, finished <-chan struct{}
	release             func()
}

func (s *providerIconSuite) blockedIconSource(onRequest func()) *blockedProviderIconFixture {
	requested, finished := make(chan struct{}, 1), make(chan struct{}, 1)
	unblock := make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	server := ownedProviderUpdateServer(s.T(), func(_ http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		if onRequest != nil {
			onRequest()
		}
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
		select {
		case finished <- struct{}{}:
		default:
		}
	})
	// Registered after the server cleanup so a fatal assertion releases it first.
	s.T().Cleanup(release)
	return &blockedProviderIconFixture{
		source:    s.writeSource(&provider.ProviderConfig{Icon: server.URL + "/icon.svg"}, nil),
		requested: requested, finished: finished, release: release,
	}
}

func (f *blockedProviderIconFixture) requireFinished(s *providerIconSuite) {
	for _, signal := range []<-chan struct{}{f.requested, f.finished} {
		select {
		case <-signal:
		case <-time.After(updateTestHangGuard):
			f.release()
			s.T().Fatal("owned icon HTTP request did not start and finish")
		}
	}
}
