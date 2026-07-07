package selfupdate

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/google/go-github/v89/github"
)

const releasesPerPage = 100

// allPagesSource lists releases across all pages so a target release behind
// more than one page of pre-releases is still found.
type allPagesSource struct {
	*selfupdate.GitHubSource
	api *github.Client
}

func newAllPagesSource() (*allPagesSource, error) {
	base, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return nil, err
	}
	var opts []github.ClientOptionsFunc
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		opts = append(opts, github.WithAuthToken(token))
	}
	client, err := github.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &allPagesSource{GitHubSource: base, api: client}, nil
}

func (s *allPagesSource) ListReleases(
	ctx context.Context,
	repository selfupdate.Repository,
) ([]selfupdate.SourceRelease, error) {
	owner, repo, err := repository.GetSlug()
	if err != nil {
		return nil, err
	}

	opts := &github.ListOptions{PerPage: releasesPerPage}
	var releases []selfupdate.SourceRelease
	for {
		page, resp, err := s.api.Repositories.ListReleases(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list releases for %s/%s: %w", owner, repo, err)
		}
		for _, rel := range page {
			releases = append(releases, newSourceRelease(rel))
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return releases, nil
}

// sourceRelease adapts a go-github RepositoryRelease to selfupdate.SourceRelease.
// It exists so this package can track a newer go-github version than the one
// go-selfupdate's NewGitHubRelease is compiled against.
type sourceRelease struct {
	*github.RepositoryRelease
}

func newSourceRelease(rel *github.RepositoryRelease) sourceRelease {
	return sourceRelease{RepositoryRelease: rel}
}

func (r sourceRelease) GetReleaseNotes() string { return r.GetBody() }
func (r sourceRelease) GetURL() string          { return r.GetHTMLURL() }
func (r sourceRelease) GetPublishedAt() time.Time {
	return r.RepositoryRelease.GetPublishedAt().Time
}

func (r sourceRelease) GetAssets() []selfupdate.SourceAsset {
	assets := make([]selfupdate.SourceAsset, len(r.Assets))
	for i, a := range r.Assets {
		assets[i] = sourceAsset{ReleaseAsset: a}
	}
	return assets
}

// sourceAsset adapts a go-github ReleaseAsset to selfupdate.SourceAsset.
type sourceAsset struct {
	*github.ReleaseAsset
}

func (a sourceAsset) GetBrowserDownloadURL() string {
	return a.ReleaseAsset.GetBrowserDownloadURL()
}

var (
	_ selfupdate.SourceRelease = sourceRelease{}
	_ selfupdate.SourceAsset   = sourceAsset{}
)
