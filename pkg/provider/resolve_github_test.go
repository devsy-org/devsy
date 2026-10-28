package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/suite"
)

const (
	testReleaseRepo     = "acme/widget"
	testReleaseManifest = "name: widget\nversion: 1.0.0\nicon: assets/logo.svg\nexec:\n  command: echo hi\n"
)

// releaseServer fakes GitHub's API, release downloads and raw content so that
// "latest" can move between manifest selection and icon download.
type releaseServer struct {
	*httptest.Server
	mu          sync.Mutex
	latest      string
	latestCalls int
	apiStatus   int
	logos       map[string]string
	requests    []string
}

func (r *releaseServer) setLatest(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latest = tag
}

func (r *releaseServer) seen(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.requests, path)
}

func (r *releaseServer) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests = append(r.requests, req.URL.Path)
	latest, apiStatus := r.latest, r.apiStatus
	r.mu.Unlock()

	path := req.URL.Path
	switch {
	case path == "/repos/"+testReleaseRepo+"/releases/latest":
		r.handleLatest(w, latest, apiStatus)
	case path == "/"+testReleaseRepo+"/releases/latest/download/provider.yaml":
		_, _ = w.Write([]byte(testReleaseManifest))
	case strings.HasPrefix(path, "/"+testReleaseRepo+"/releases/download/") &&
		strings.HasSuffix(path, "/provider.yaml"):
		tag := strings.TrimSuffix(
			strings.TrimPrefix(path, "/"+testReleaseRepo+"/releases/download/"),
			"/provider.yaml",
		)
		if _, found := r.logos[tag]; !found {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte(testReleaseManifest))
	default:
		r.handleLogo(w, req)
	}
}

func (r *releaseServer) handleLatest(w http.ResponseWriter, latest string, apiStatus int) {
	r.mu.Lock()
	r.latestCalls++
	r.mu.Unlock()
	if apiStatus != 0 {
		w.WriteHeader(apiStatus)
		return
	}
	_, _ = w.Write([]byte(`{"tag_name":"` + latest + `","prerelease":false}`))
}

func (r *releaseServer) handleLogo(w http.ResponseWriter, req *http.Request) {
	const marker = "/" + testReleaseRepo + "/refs/tags/"
	path := req.URL.Path
	if tag, rest, ok := strings.Cut(strings.TrimPrefix(path, marker), "/"); ok &&
		strings.HasPrefix(path, marker) && rest == "assets/logo.svg" {
		if logo, found := r.logos[tag]; found {
			_, _ = w.Write([]byte(logo))
			return
		}
	}
	http.NotFound(w, req)
}

type ResolveProvenanceSuite struct {
	suite.Suite
	server *releaseServer
}

func TestResolveProvenanceSuite(t *testing.T) {
	suite.Run(t, new(ResolveProvenanceSuite))
}

func (s *ResolveProvenanceSuite) SetupTest() {
	setupTestHome(s.T())

	s.server = &releaseServer{
		latest: testTagV100,
		logos: map[string]string{
			testTagV100: testIconSVGLight,
			"v2.0.0":    testIconSVGDark,
		},
	}
	s.server.Server = httptest.NewServer(http.HandlerFunc(s.server.handle))
	s.T().Cleanup(s.server.Close)

	previousWeb, previousAPI, previousRaw := githubWebBaseURL, githubAPIBaseURL, githubRawBaseURL
	githubWebBaseURL, githubAPIBaseURL, githubRawBaseURL = s.server.URL, s.server.URL, s.server.URL
	s.T().Cleanup(func() {
		githubWebBaseURL, githubAPIBaseURL, githubRawBaseURL = previousWeb, previousAPI, previousRaw
	})
}

func (s *ResolveProvenanceSuite) TestUnpinnedSourcePinsManifestAndIconToOneRelease() {
	cfg, _ := s.install("github.com/" + testReleaseRepo)

	s.Equal("github.com/"+testReleaseRepo, cfg.Source.Raw, "unpinned Raw is preserved for updates")
	s.Equal(testReleaseRepo, cfg.Source.Github)
	s.Equal(testTagV100, cfg.Source.ResolvedRef)
	s.Equal(
		s.server.URL+"/"+testReleaseRepo+"/releases/download/v1.0.0/provider.yaml",
		cfg.Source.ResolvedManifestURL,
	)
	s.True(s.server.seen("/" + testReleaseRepo + "/releases/download/v1.0.0/provider.yaml"))
	s.False(s.server.seen("/" + testReleaseRepo + "/releases/latest/download/provider.yaml"))

	installedIcon := s.iconText(cfg)

	s.server.setLatest("v2.0.0")
	s.Equal(installedIcon, s.iconText(cfg), "latest moving must not change the installed icon")
	s.Equal(testTagV100, cfg.Source.ResolvedRef)

	next, _ := s.install("github.com/" + testReleaseRepo)
	s.Equal("v2.0.0", next.Source.ResolvedRef)
	s.NotEqual(installedIcon, s.iconText(next))
	s.NotEqual(cfg.IconRevision, next.IconRevision)
}

func (s *ResolveProvenanceSuite) TestLatestUsesDesignatedEndpointOnly() {
	s.install("github.com/" + testReleaseRepo)

	s.True(s.server.seen("/repos/" + testReleaseRepo + "/releases/latest"))
	s.False(
		s.server.seen("/repos/"+testReleaseRepo+"/releases"),
		"the newest-published list is never used",
	)
}

func (s *ResolveProvenanceSuite) TestLatestTagIsEscapedInManifestRequest() {
	for _, tt := range []struct {
		tag     string
		escaped string
	}{
		{tag: "release#1", escaped: "release%231"},
		{tag: "release%2F1", escaped: "release%252F1"},
	} {
		s.Run(tt.tag, func() {
			s.server.mu.Lock()
			s.server.latest = tt.tag
			s.server.logos[tt.tag] = testIconSVGLight
			s.server.mu.Unlock()

			cfg, _ := s.install("github.com/" + testReleaseRepo)

			s.True(s.server.seen("/repos/" + testReleaseRepo + "/releases/latest"))
			s.True(
				s.server.seen(
					"/" + testReleaseRepo + "/releases/download/" + tt.tag + "/provider.yaml",
				),
			)
			s.Equal(tt.tag, cfg.Source.ResolvedRef)
			s.Equal(
				s.server.URL+"/"+testReleaseRepo+"/releases/download/"+tt.escaped+"/provider.yaml",
				cfg.Source.ResolvedManifestURL,
			)
			s.Equal(testIconSVGLight, s.decoded(s.iconText(cfg)))
		})
	}
}

func (s *ResolveProvenanceSuite) TestExplicitTagIsRetainedWithoutMetadataLookup() {
	cfg, _ := s.install("github.com/" + testReleaseRepo + "@v2.0.0")

	s.Equal("v2.0.0", cfg.Source.ResolvedRef)
	s.Zero(s.server.latestCalls, "an explicit tag needs no latest-release lookup")
	s.Equal(
		s.server.URL+"/"+testReleaseRepo+"/releases/download/v2.0.0/provider.yaml",
		cfg.Source.ResolvedManifestURL,
	)
	s.Equal(testIconSVGDark, s.decoded(s.iconText(cfg)))
}

func (s *ResolveProvenanceSuite) TestMetadataFailureLeavesProvenanceUnresolved() {
	s.server.mu.Lock()
	s.server.apiStatus = http.StatusInternalServerError
	s.server.mu.Unlock()

	cfg, _ := s.install("github.com/" + testReleaseRepo)

	s.Equal(testReleaseRepo, cfg.Source.Github, "the provider still installs")
	s.Empty(cfg.Source.ResolvedRef)
	s.Empty(cfg.Source.ResolvedManifestURL)
	s.True(s.server.seen("/" + testReleaseRepo + "/releases/latest/download/provider.yaml"))
	s.False(s.server.seen("/" + testReleaseRepo + "/refs/tags/v1.0.0/assets/logo.svg"))

	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.True(result.Missing, "repository-relative icons are disabled without provenance")
}

func (s *ResolveProvenanceSuite) TestMetadataFailureStillAllowsAbsoluteIcons() {
	s.server.mu.Lock()
	s.server.apiStatus = http.StatusInternalServerError
	s.server.mu.Unlock()

	raw, resolved, err := ResolveProvider(context.Background(), "github.com/"+testReleaseRepo)
	s.Require().NoError(err)
	cfg, err := ParseAuthorProvider(bytes.NewReader(raw))
	s.Require().NoError(err)
	cfg.Source = *resolved
	cfg.Icon = s.server.URL + "/" + testReleaseRepo + "/refs/tags/v1.0.0/assets/logo.svg"

	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, raw))
	s.Equal(testIconSVGLight, s.decoded(s.iconText(cfg)))
}

func (s *ResolveProvenanceSuite) TestDirectURLKeepsManifestQueryOutOfProvenance() {
	var sawToken bool
	manifest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawToken = r.URL.Query().Get("token") == "secret"
		_, _ = w.Write([]byte(testReleaseManifest))
	}))
	s.T().Cleanup(manifest.Close)

	_, source, err := ResolveProvider(
		context.Background(),
		manifest.URL+"/dir/provider.yaml?token=secret",
	)
	s.Require().NoError(err)

	s.True(sawToken, "manifest request keeps its query authentication")
	s.Equal(manifest.URL+"/dir/provider.yaml?token=secret", source.URL)
	s.Equal(manifest.URL+"/dir/provider.yaml", source.ResolvedManifestURL)
	s.Empty(source.ResolvedRef)

	location, err := ResolveProviderIconReference(*source, "assets/logo.svg")
	s.Require().NoError(err)
	s.Equal(manifest.URL+"/dir/assets/logo.svg", location.URL)
}

func (s *ResolveProvenanceSuite) TestLocalYAMLRetainsActualBase() {
	dir := s.T().TempDir()
	file := filepath.Join(dir, "provider.yaml")
	s.Require().NoError(os.WriteFile(file, []byte(testReleaseManifest), 0o600))

	_, source, err := ResolveProvider(context.Background(), file)
	s.Require().NoError(err)

	s.Equal(file, source.File)
	s.Empty(source.ResolvedRef)
	s.Empty(source.ResolvedManifestURL)
}

type tokenResolver struct{ token string }

func (r tokenResolver) Resolve(
	_ context.Context,
	protocol, host, path string,
) (string, string, error) {
	if protocol != "https" || host != "github.com" || path != "/"+testReleaseRepo {
		return "", "", fmt.Errorf("unexpected credential context %s://%s%s", protocol, host, path)
	}
	return "test-user", r.token, nil
}

func (s *ResolveProvenanceSuite) TestLatestLookupRetriesWithCredentialsAfterDenial() {
	var attempts []string
	var mu sync.Mutex
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts = append(attempts, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != testIconAuthorization {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v7"}`))
	}))
	s.T().Cleanup(api.Close)
	previous := githubAPIBaseURL
	githubAPIBaseURL = api.URL
	s.T().Cleanup(func() { githubAPIBaseURL = previous })

	_, err := FetchLatestGitHubReleaseTag(context.Background(), testReleaseRepo, nil)
	s.Require().Error(err)

	tag, err := FetchLatestGitHubReleaseTag(
		context.Background(), testReleaseRepo, tokenResolver{token: testIconToken},
	)
	s.Require().NoError(err)
	s.Equal("v7", tag)

	mu.Lock()
	gotAttempts := append([]string(nil), attempts...)
	mu.Unlock()
	s.Equal([]string{"", "", testIconAuthorization}, gotAttempts)

	var iconPath, iconAuthorization string
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iconPath = r.URL.Path
		iconAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(testIconSVGLight))
	}))
	s.T().Cleanup(raw.Close)
	previousRaw := githubRawBaseURL
	githubRawBaseURL = raw.URL
	s.T().Cleanup(func() { githubRawBaseURL = previousRaw })

	cfg, err := ParseAuthorProvider(strings.NewReader(testReleaseManifest))
	s.Require().NoError(err)
	cfg.Source = ProviderSource{
		Raw:                 "github.com/" + testReleaseRepo,
		Github:              testReleaseRepo,
		ResolvedRef:         tag,
		ResolvedManifestURL: "https://github.com/" + testReleaseRepo + "/releases/download/" + tag + "/provider.yaml",
	}
	s.Require().NoError(
		PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil),
	)
	s.Equal(testIconSVGLight, s.decoded(s.iconText(cfg)))
	s.Equal("/"+testReleaseRepo+"/refs/tags/v7/assets/logo.svg", iconPath)
	s.Empty(iconAuthorization, "the API credential must not be forwarded to raw asset requests")
}

func (s *ResolveProvenanceSuite) TestInstalledProvenanceSurvivesSaveLoad() {
	cfg, _ := s.install("github.com/" + testReleaseRepo)
	s.Require().NoError(SaveProviderConfig(config.DefaultContext, cfg))

	loaded, err := LoadProviderConfig(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)

	s.Equal(cfg.Source, loaded.Source)
	s.Equal(cfg.IconRevision, loaded.IconRevision)
	s.NotEmpty(loaded.IconRevision)
	s.Equal(s.iconText(cfg), s.iconText(loaded))
}

func (s *ResolveProvenanceSuite) TestLatestRejectsDraftAndPrereleaseResponses() {
	for _, body := range []string{
		`{"tag_name":"v2-rc1","prerelease":true}`,
		`{"tag_name":"v2","draft":true}`,
	} {
		s.Run(body, func() {
			api := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(body))
				}),
			)
			defer api.Close()
			tag, _, err := requestLatestReleaseTag(context.Background(), api.URL, "")
			s.Require().Error(err)
			s.Empty(tag)
		})
	}
}

func (s *ResolveProvenanceSuite) install(source string) (*ProviderConfig, []byte) {
	raw, resolved, err := ResolveProvider(context.Background(), source)
	s.Require().NoError(err)
	cfg, err := ParseAuthorProvider(bytes.NewReader(raw))
	s.Require().NoError(err)
	cfg.Source = *resolved
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, raw))
	return cfg, raw
}

func (s *ResolveProvenanceSuite) iconText(cfg *ProviderConfig) string {
	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.Require().False(result.Missing, "icon should be cached")
	return result.DataBase64
}

func (s *ResolveProvenanceSuite) decoded(encoded string) string {
	s.T().Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	s.Require().NoError(err)
	return string(data)
}
