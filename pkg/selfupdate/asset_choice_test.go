package selfupdate

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/google/go-github/v92/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticReleaseSource struct {
	releases []selfupdate.SourceRelease
}

func (s staticReleaseSource) ListReleases(
	context.Context,
	selfupdate.Repository,
) ([]selfupdate.SourceRelease, error) {
	return s.releases, nil
}

func (staticReleaseSource) DownloadReleaseAsset(
	context.Context,
	*selfupdate.Release,
	int64,
) (io.ReadCloser, error) {
	return nil, fmt.Errorf("download is not used by asset detection")
}

func releaseWithAssets(names ...string) selfupdate.SourceRelease {
	assets := make([]*github.ReleaseAsset, len(names))
	for i, name := range names {
		assets[i] = &github.ReleaseAsset{
			ID:   new(int64(i + 1)),
			Name: new(name),
			BrowserDownloadURL: new(
				"https://github.com/devsy-org/devsy/releases/download/v1.2.3/" + name,
			),
		}
	}
	return newSourceRelease(&github.RepositoryRelease{
		TagName: "v1.2.3",
		Assets:  assets,
	})
}

func detectAssetName(t *testing.T, goos, goarch string, assetNames ...string) string {
	t.Helper()
	cfg := updaterConfig(staticReleaseSource{
		releases: []selfupdate.SourceRelease{releaseWithAssets(assetNames...)},
	}, false)
	cfg.OS = goos
	cfg.Arch = goarch
	updater, err := selfupdate.NewUpdater(cfg)
	require.NoError(t, err)

	release, found, err := updater.DetectLatest(
		context.Background(),
		selfupdate.ParseSlug("devsy-org/devsy"),
	)
	require.NoError(t, err)
	require.True(t, found, "no asset detected for %s/%s", goos, goarch)
	return release.AssetName
}

func TestDetectLatestSkipsHomebrewManagedAssets(t *testing.T) {
	platforms := []struct{ goos, goarch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
	}
	for _, p := range platforms {
		direct := fmt.Sprintf("devsy-%s-%s", p.goos, p.goarch)
		managed := direct + "-homebrew"
		orders := map[string][]string{
			"direct first":  {direct, managed},
			"managed first": {managed, direct},
		}
		for order, names := range orders {
			t.Run(direct+"/"+order, func(t *testing.T) {
				assert.Equal(t, direct, detectAssetName(t, p.goos, p.goarch, names...))
			})
		}
	}
}
