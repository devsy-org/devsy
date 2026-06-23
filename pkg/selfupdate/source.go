package selfupdate

import (
	"context"
	"fmt"
	"os"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/google/go-github/v74/github"
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
	client := github.NewClient(nil)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		client = client.WithAuthToken(token)
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
			releases = append(releases, selfupdate.NewGitHubRelease(rel))
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return releases, nil
}
