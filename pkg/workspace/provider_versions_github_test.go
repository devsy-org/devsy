package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListGitHubReleases_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/devsy-org/provider-aws/releases" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]githubRelease{
			{
				TagName:     "v1.2.0",
				PublishedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
				Prerelease:  false,
				Draft:       false,
			},
			{
				TagName:     "v1.2.0-rc1",
				PublishedAt: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
				Prerelease:  true,
				Draft:       false,
			},
			{
				TagName:     "v1.1.0",
				PublishedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
				Prerelease:  false,
				Draft:       false,
			},
			{TagName: "draft-1", Draft: true},
		})
	}))
	defer server.Close()

	versions, err := listGitHubReleases(server.URL, "devsy-org", "provider-aws", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 non-prerelease releases, got %d", len(versions))
	}
	if versions[0].Tag != "v1.2.0" {
		t.Fatalf("expected newest first, got %q", versions[0].Tag)
	}
}

func TestListGitHubReleases_IncludePrerelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]githubRelease{
			{TagName: "v1.2.0-rc1", Prerelease: true, PublishedAt: time.Now()},
		})
	}))
	defer server.Close()

	versions, err := listGitHubReleases(server.URL, "x", "y", true)
	if err != nil || len(versions) != 1 || !versions[0].Prerelease {
		t.Fatalf("prerelease must be included when flag set: %+v %v", versions, err)
	}
}

func TestListGitHubReleases_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()
	_, err := listGitHubReleases(server.URL, "x", "y", false)
	if err == nil || !errors.Is(err, ErrVersionListRateLimited) {
		t.Fatalf("expected rate-limited sentinel, got %v", err)
	}
}

func TestParseGitHubSourcePath(t *testing.T) {
	org, repo, ok := parseGitHubSourcePath("github.com/devsy-org/provider-aws@v1.0.0")
	if !ok || org != "devsy-org" || repo != "provider-aws" {
		t.Fatalf("got org=%q repo=%q ok=%v", org, repo, ok)
	}
	org, repo, ok = parseGitHubSourcePath("github.com/devsy-org/provider-aws")
	if !ok || org != "devsy-org" || repo != "provider-aws" {
		t.Fatalf("got org=%q repo=%q ok=%v", org, repo, ok)
	}
	if _, _, ok := parseGitHubSourcePath("github.com/onlyorg"); ok {
		t.Fatal("missing repo segment must fail")
	}
}
