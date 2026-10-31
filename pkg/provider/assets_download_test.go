package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/suite"
	"golang.org/x/image/webp"
)

const svgOpen = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`

type fakeCredentials struct {
	mu    sync.Mutex
	calls []string
	token string
}

func (f *fakeCredentials) Resolve(
	_ context.Context,
	protocol, host, _ string,
) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, protocol+"://"+host)
	return "user", f.token, nil
}

func (f *fakeCredentials) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type pathSensitiveCredentials struct {
	tokens map[string]string
	paths  []string
}

func (f *pathSensitiveCredentials) Resolve(
	_ context.Context,
	_, _, path string,
) (string, string, error) {
	f.paths = append(f.paths, path)
	return "user", f.tokens[path], nil
}

func lossyWebPBytes(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(`
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
`)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type iconRoundTripper func(*http.Request) (*http.Response, error)

func (f iconRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type AssetDownloadSuite struct {
	suite.Suite
}

func TestAssetDownloadSuite(t *testing.T) {
	suite.Run(t, new(AssetDownloadSuite))
}

func (s *AssetDownloadSuite) SetupTest() {
	setupTestHome(s.T())
}

func (s *AssetDownloadSuite) TestValidateAcceptsSupportedFormats() {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngBytes(s.T(), 16, 16), mimePNG},
		{"png at the pixel limit", pngBytes(s.T(), maxIconDimension, 1), mimePNG},
		{"jpeg", jpegBytes(s.T(), 16, 16), mimeJPEG},
		{"webp", validWebPBytes(s.T()), mimeWebP},
		{
			"extended webp with matching frame",
			webpExtendedFrameBytes(1, 1, validWebPBytes(s.T())),
			mimeWebP,
		},
		{"svg", []byte(testIconSVGLight), mimeSVG},
		{"svg with BOM and declaration", append(
			[]byte("\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"),
			[]byte(testIconSVGLight)...,
		), mimeSVG},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := validateIconImage(tt.data, "")
			s.Require().NoError(err)
			s.Equal(tt.want, got)
		})
	}
}

func (s *AssetDownloadSuite) TestValidateRejectsBadImages() {
	truncatedPNG := pngBytes(s.T(), 4, 4)
	truncatedPNG = truncatedPNG[:len(truncatedPNG)-12]
	truncatedJPEG := jpegBytes(s.T(), 4, 4)
	truncatedJPEG = truncatedJPEG[:len(truncatedJPEG)-2]
	truncatedWebP := validWebPBytes(s.T())
	truncatedWebP = truncatedWebP[:len(truncatedWebP)-2]
	_, err := png.DecodeConfig(bytes.NewReader(truncatedPNG))
	s.Require().NoError(err)
	_, err = jpeg.DecodeConfig(bytes.NewReader(truncatedJPEG))
	s.Require().NoError(err)
	_, err = webp.DecodeConfig(bytes.NewReader(truncatedWebP))
	s.Require().NoError(err)

	tests := []struct {
		name        string
		data        []byte
		contentType string
	}{
		{"empty image body", nil, ""},
		{"html error page", []byte("<!DOCTYPE html><html><body>404</body></html>"), "text/html"},
		{
			"html login page labelled as image",
			[]byte("<html><body>login</body></html>"),
			"image/png",
		},
		{
			"html declared type with svg-looking body",
			[]byte(testIconSVGLight),
			"text/html; charset=utf-8",
		},
		{"plain text", []byte("not an image"), ""},
		{"gif", []byte("GIF89a\x01\x00\x01\x00"), ""},
		{"png too wide", pngBytes(s.T(), maxIconDimension+1, 1), ""},
		{"png too tall", pngBytes(s.T(), 1, maxIconDimension+1), ""},
		{"jpeg too wide", jpegBytes(s.T(), maxIconDimension+1, 8), ""},
		{"webp too wide", webpExtendedBytes(maxIconDimension+1, 8), ""},
		{"truncated png", pngBytes(s.T(), 4, 4)[:12], ""},
		{"truncated webp", webpExtendedBytes(8, 8)[:20], ""},
		{"truncated PNG payload", truncatedPNG, ""},
		{"truncated JPEG payload", truncatedJPEG, ""},
		{"truncated WebP payload", truncatedWebP, ""},
		{
			"oversized",
			append([]byte(testIconSVGLight), bytes.Repeat([]byte(" "), maxIconBytes)...),
			"",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := validateIconImage(tt.data, tt.contentType)
			s.Error(err)
		})
	}
}

func (s *AssetDownloadSuite) TestValidateRejectsWebPCanvasFrameMismatch() {
	data := webpExtendedFrameBytes(2, 1, validWebPBytes(s.T()))

	_, err := validateIconImage(data, "")

	s.Error(err, "the VP8X canvas must match its VP8L frame")
}

func (s *AssetDownloadSuite) TestValidateRejectsOversizedWebPInnerFrameBeforeDecode() {
	data := webpExtendedFrameBytes(1, 1, oversizedWebPFrameBytes(s.T()))

	_, err := validateIconImage(data, "")

	s.Error(err)
}

func (s *AssetDownloadSuite) TestValidateRejectsOversizedVP8ChunkLength() {
	data := lossyWebPBytes(s.T())
	s.Require().Equal("VP8 ", string(data[12:16]))
	mimeType, err := validateIconImage(data, "")
	s.Require().NoError(err)
	s.Equal(mimeWebP, mimeType)

	binary.LittleEndian.PutUint32(data[16:20], 2<<20)
	binary.LittleEndian.PutUint32(data[4:8], (2<<20)+12)
	_, err = validateIconImage(data, "")
	s.Error(err)
}

func (s *AssetDownloadSuite) TestValidateSVGAcceptsSimpleDrawings() {
	accepted := map[string]string{
		"gradient fill": svgOpen + `<defs><linearGradient id="g"><stop offset="0" stop-color="#000"/>` +
			`</linearGradient></defs><rect fill="url(#g)" width="4" height="4"/></svg>`,
		"internal use":    svgOpen + `<defs><path id="a" d="M0 0h4v4z"/></defs><use href="#a"/></svg>`,
		"xlink internal":  svgOpen + `<defs><path id="a" d="M0 0h4v4z"/></defs><use xlink:href="#a"/></svg>`,
		"stylesheet":      svgOpen + `<style>.a{fill:red}.b{fill:url(#g)}</style><path class="a" d="M0 0"/></svg>`,
		"style attribute": svgOpen + `<rect style="fill:url('#g');stroke:#000" width="1" height="1"/></svg>`,
		"editor metadata": `<svg xmlns="http://www.w3.org/2000/svg" ` +
			`xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape">` +
			`<inkscape:namedview id="n"/><circle r="2"/></svg>`,
		"animation of fill": svgOpen + `<rect width="1" height="1">` +
			`<animate attributeName="fill" values="red;blue" dur="1s"/></rect></svg>`,
		"xml comment":        `<!-- logo -->` + svgOpen + `<circle r="2"/></svg>`,
		"split internal url": svgOpen + `<style>.a{fill:u<![CDATA[rl]]>(#g)}</style></svg>`,
	}

	for name, svg := range accepted {
		s.Run(name, func() {
			_, err := validateIconImage([]byte(svg), "image/svg+xml")
			s.Require().NoError(err)
		})
	}
}

func (s *AssetDownloadSuite) TestValidateSVGRejectsHostileContent() {
	rejected := map[string]string{
		"not well-formed":   svgOpen + `<rect></svg>`,
		"unterminated":      svgOpen + `<rect/>`,
		"wrong root":        `<html xmlns="http://www.w3.org/2000/svg"></html>`,
		"missing namespace": `<svg><rect/></svg>`,
		"multiple roots":    svgOpen + `</svg>` + svgOpen + `</svg>`,
		"text outside root": `hello ` + svgOpen + `</svg>`,
		"doctype": `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" ` +
			`"http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">` + svgOpen + `</svg>`,
		"entity expansion": `<!DOCTYPE svg [<!ENTITY a "aaaa"><!ENTITY b "&a;&a;&a;&a;">]>` +
			svgOpen + `<text>&b;</text></svg>`,
		"external entity": `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>` +
			svgOpen + `<text>&x;</text></svg>`,
		"stylesheet instruction":    `<?xml-stylesheet href="https://evil.example/a.css"?>` + svgOpen + `</svg>`,
		"script":                    svgOpen + `<script>alert(1)</script></svg>`,
		"nested script":             svgOpen + `<g><SCRIPT>alert(1)</SCRIPT></g></svg>`,
		"foreignObject":             svgOpen + `<foreignObject><div/></foreignObject></svg>`,
		"iframe":                    svgOpen + `<iframe src="https://evil.example"/></svg>`,
		"object":                    svgOpen + `<object data="x"/></svg>`,
		"embed":                     svgOpen + `<embed src="x"/></svg>`,
		"image element":             svgOpen + `<image href="data:image/png;base64,AAAA"/></svg>`,
		"feImage":                   svgOpen + `<filter><feImage href="#a"/></filter></svg>`,
		"xhtml element":             svgOpen + `<div xmlns="http://www.w3.org/1999/xhtml"/></svg>`,
		"event handler":             svgOpen + `<rect onload="alert(1)"/></svg>`,
		"event handler mixed":       svgOpen + `<rect OnClick="alert(1)"/></svg>`,
		"external use":              svgOpen + `<use href="https://evil.example/a.svg#x"/></svg>`,
		"external xlink":            svgOpen + `<use xlink:href="//evil.example/a.svg#x"/></svg>`,
		"relative file href":        svgOpen + `<use href="other.svg#x"/></svg>`,
		"javascript href":           svgOpen + `<a href="javascript:alert(1)"><rect/></a></svg>`,
		"external fill":             svgOpen + `<rect fill="url(https://evil.example/x)"/></svg>`,
		"external style attr":       svgOpen + `<rect style="fill:url( 'http://evil.example/x' )"/></svg>`,
		"css import":                svgOpen + `<style>@import url(https://evil.example/a.css);</style></svg>`,
		"css external url":          svgOpen + `<style>.a{fill:url(https://evil.example/x)}</style></svg>`,
		"css escaped url":           svgOpen + `<style>.a{fill:\75rl(https://evil.example/x)}</style></svg>`,
		"css expression":            svgOpen + `<rect style="width:expression(alert(1))"/></svg>`,
		"animate href":              svgOpen + `<a><animate attributeName="href" values="javascript:alert(1)"/></a></svg>`,
		"set href":                  svgOpen + `<a><set attributeName="xlink:href" to="https://evil.example"/></a></svg>`,
		"split css import":          svgOpen + `<style>@im<![CDATA[port]]> "https://evil.example/x.css";</style></svg>`,
		"split css url":             svgOpen + `<style>.a{fill:u<![CDATA[rl]]>(https://evil.example/x)}</style></svg>`,
		"comment split css url":     svgOpen + `<style>.a{fill:u<!-- split -->rl(https://evil.example/x)}</style></svg>`,
		"stylesheet nested element": svgOpen + `<style><g/>.a{fill:red}</style></svg>`,
		"external base on fragment": svgOpen + `<use href="#id" xml:base="https://evil.example/x.svg"/></svg>`,
		"inherited external base": `<svg xmlns="http://www.w3.org/2000/svg" ` +
			`xml:base="https://evil.example/x.svg"><use href="#id"/></svg>`,
		"non-utf8 encoding": `<?xml version="1.0" encoding="ISO-8859-1"?>` + svgOpen + `</svg>`,
	}

	for name, svg := range rejected {
		s.Run(name, func() {
			_, err := validateIconImage([]byte(svg), "image/svg+xml")
			s.Error(err)
		})
	}
}

func (s *AssetDownloadSuite) TestDownloadSuccess() {
	var userAgent string
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.UserAgent()
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(testIconSVGLight))
	})

	variant := s.fetcher(nil).fetchVariant(
		context.Background(), ProviderSource{}, srv.URL+"/logo.svg",
	)

	s.Empty(variant.failure)
	s.Equal(mimeSVG, variant.mime)
	s.NotEmpty(userAgent)
}

func (s *AssetDownloadSuite) TestDownloadFailuresAreDiagnosedNotFatal() {
	srv := s.serve(s.downloadFailureHandler())

	for _, path := range []string{
		"/missing", "/html", "/png-as-html", "/declared-big", "/streamed-big",
		"/truncated-png", "/truncated-jpeg", "/truncated-webp", "/broken.svg",
		"/huge.png", "/error",
	} {
		s.Run(path, func() {
			cfg := s.widgetWithIcons(srv.URL+path, srv.URL+"/ok.svg")

			s.Require().NoError(PrepareProviderIcons(
				context.Background(), config.DefaultContext, cfg, nil,
			))
			s.NotEmpty(cfg.IconRevision)

			broken, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
			s.Require().NoError(err)
			s.True(broken.Missing)

			working, err := ReadProviderIcon(config.DefaultContext, cfg, "dark")
			s.Require().NoError(err)
			s.False(working.Missing, "a bad variant must not take the good one down")
		})
	}
}

func (s *AssetDownloadSuite) TestDownloadTimeout() {
	release := make(chan struct{})
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	s.T().Cleanup(func() { close(release) })
	fetcher := s.fetcher(nil)
	fetcher.timeout = 150 * time.Millisecond

	start := time.Now()
	variant := fetcher.fetchVariant(context.Background(), ProviderSource{}, srv.URL+"/slow.svg")

	s.Contains(variant.failure, "timed out")
	s.Less(time.Since(start), 2*time.Second)
	s.Nil(variant.data)
}

func (s *AssetDownloadSuite) TestDownloadHonorsCancellation() {
	started := make(chan struct{})
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	variant := s.fetcher(nil).fetchVariant(ctx, ProviderSource{}, srv.URL+"/slow.svg")

	s.Contains(variant.failure, "canceled")
}

func (s *AssetDownloadSuite) TestRedirectLimit() {
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		hop := 0
		if len(r.URL.Path) > len("/hop/") {
			hop = int(r.URL.Path[len("/hop/")] - '0')
		}
		if hop > 0 {
			http.Redirect(w, r, "/hop/"+string(rune('0'+hop-1)), http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(testIconSVGLight))
	})

	within := s.fetcher(nil).fetchVariant(context.Background(), ProviderSource{}, srv.URL+"/hop/5")
	s.Empty(within.failure, "five redirects are allowed")

	beyond := s.fetcher(nil).fetchVariant(context.Background(), ProviderSource{}, srv.URL+"/hop/6")
	s.Contains(beyond.failure, "redirects")
	s.Nil(beyond.data)
}

func (s *AssetDownloadSuite) TestAuthorizationNeverCrossesOrigins() {
	var otherAuth []string
	var mu sync.Mutex
	other := s.serve(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		otherAuth = append(otherAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(testIconSVGLight))
	})
	var originAuth []string
	origin := s.serve(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		originAuth = append(originAuth, auth)
		mu.Unlock()
		if auth == "" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, other.URL+"/logo.svg", http.StatusFound)
	})
	credentials := &fakeCredentials{token: "s3cr3t"}

	variant := s.fetcher(credentials).fetchVariant(
		context.Background(), ProviderSource{}, origin.URL+"/private.svg",
	)

	s.Empty(variant.failure)
	mu.Lock()
	defer mu.Unlock()
	s.Equal([]string{"", "Bearer s3cr3t"}, originAuth, "anonymous first, then credentialed")
	s.Equal([]string{""}, otherAuth, "the off-origin redirect target must see no Authorization")
	s.Equal(1, credentials.callCount())
}

func (s *AssetDownloadSuite) TestAuthorizationKeptOnSameOriginRedirect() {
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != testIconAuthorization {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/start.svg" {
			http.Redirect(w, r, "/final.svg", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(testIconSVGLight))
	})

	variant := s.fetcher(&fakeCredentials{token: testIconToken}).fetchVariant(
		context.Background(), ProviderSource{}, srv.URL+"/start.svg",
	)

	s.Empty(variant.failure)
}

func (s *AssetDownloadSuite) TestCredentialsOnlyAfterAnonymousDenial() {
	srv := s.serve(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(testIconSVGLight))
	})
	credentials := &fakeCredentials{token: testIconToken}

	variant := s.fetcher(credentials).fetchVariant(
		context.Background(), ProviderSource{}, srv.URL+"/public.svg",
	)

	s.Empty(variant.failure)
	s.Zero(credentials.callCount(), "public icons must never trigger credential lookup")
}

func (s *AssetDownloadSuite) TestDeniedPrivateIconFallsBackWithoutError() {
	srv := s.serve(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	cfg := s.widgetWithIcons(srv.URL+"/private.svg", "")
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.True(result.Missing)
}

func (s *AssetDownloadSuite) TestCredentialTransportSafe() {
	tests := map[string]bool{
		"https://example.com/a.svg": true,
		"http://example.com/a.svg":  false,
		"http://127.0.0.1:8080/a":   true,
		"http://[::1]:8080/a":       true,
		"http://localhost/a":        true,
		"http://10.0.0.5/a":         false,
	}

	for raw, want := range tests {
		parsed, err := url.Parse(raw)
		s.Require().NoError(err)
		s.Equal(want, credentialTransportSafe(parsed), raw)
	}
}

func (s *AssetDownloadSuite) TestPrivateGithubIconUsesRepositoryScopedCredentials() {
	credentials := &pathSensitiveCredentials{
		tokens: map[string]string{"/owner/repo": "scoped-token"},
	}
	authByPath := make(map[string][]string)
	client := &http.Client{
		Transport: iconRoundTripper(func(request *http.Request) (*http.Response, error) {
			authorization := request.Header.Get("Authorization")
			authByPath[request.URL.Path] = append(authByPath[request.URL.Path], authorization)
			response := &http.Response{
				StatusCode:    http.StatusForbidden,
				Header:        make(http.Header),
				Body:          io.NopCloser(strings.NewReader("denied")),
				ContentLength: -1,
				Request:       request,
			}
			if request.URL.Path == "/owner/repo/assets/icon.svg" &&
				authorization == "Bearer scoped-token" {
				response.StatusCode = http.StatusOK
				response.Header.Set("Content-Type", "image/svg+xml")
				response.Body = io.NopCloser(strings.NewReader(testIconSVGLight))
			}
			return response, nil
		}),
	}
	fetcher := &iconFetcher{
		client:      client,
		credentials: githubIconCredentials{inner: credentials},
		timeout:     2 * time.Second,
	}
	s.T().Cleanup(fetcher.close)

	owned := fetcher.fetchVariant(
		context.Background(),
		ProviderSource{},
		"https://raw.githubusercontent.com/owner/repo/assets/icon.svg",
	)
	foreign := fetcher.fetchVariant(
		context.Background(),
		ProviderSource{},
		"https://raw.githubusercontent.com/other/repo/assets/icon.svg",
	)

	s.Empty(owned.failure)
	s.Equal(mimeSVG, owned.mime)
	s.Equal([]byte(testIconSVGLight), owned.data)
	s.Contains(foreign.failure, "HTTP status 403")
	s.NotContains(
		strings.Join(authByPath["/other/repo/assets/icon.svg"], "\n"),
		"Bearer scoped-token",
	)
}

func (s *AssetDownloadSuite) TestGithubIconRejectsEncodedControlCredentialPaths() {
	for _, encodedOwner := range []string{
		"owner%0Ausername%3Dattacker",
		"owner%0Dusername%3Dattacker",
	} {
		s.Run(encodedOwner, func() {
			credentials := &pathSensitiveCredentials{
				tokens: map[string]string{
					"/owner\nusername=attacker/repo": "test-token",
					"/owner\rusername=attacker/repo": "test-token",
				},
			}
			var authorizationHeaders []string
			client := &http.Client{
				Transport: iconRoundTripper(func(request *http.Request) (*http.Response, error) {
					authorizationHeaders = append(
						authorizationHeaders,
						request.Header.Get("Authorization"),
					)
					return &http.Response{
						StatusCode:    http.StatusForbidden,
						Header:        make(http.Header),
						Body:          io.NopCloser(strings.NewReader("denied")),
						ContentLength: -1,
						Request:       request,
					}, nil
				}),
			}
			fetcher := &iconFetcher{
				client:      client,
				credentials: githubIconCredentials{inner: credentials},
				timeout:     2 * time.Second,
			}
			s.T().Cleanup(fetcher.close)

			variant := fetcher.fetchVariant(
				context.Background(),
				ProviderSource{},
				"https://raw.githubusercontent.com/"+encodedOwner+"/repo/assets/icon.svg",
			)

			s.NotEmpty(variant.failure)
			s.Empty(credentials.paths)
			for _, authorization := range authorizationHeaders {
				s.Empty(authorization)
			}
		})
	}
}

func (s *AssetDownloadSuite) TestDiagnosticsRedactURLs() {
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loop" {
			http.Redirect(w, r, "/loop?sig=redirect-secret", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	parsed, err := url.Parse(srv.URL)
	s.Require().NoError(err)
	parsed.User = url.UserPassword("admin", "hunter2")
	withUserinfo := parsed.String()

	for _, ref := range []string{
		srv.URL + "/gone.svg?token=query-secret&sig=abc#frag",
		withUserinfo + "/gone.svg?token=query-secret",
		srv.URL + "/loop?sig=initial-secret",
	} {
		variant := s.fetcher(nil).fetchVariant(context.Background(), ProviderSource{}, ref)

		s.NotEmpty(variant.failure)
		for _, secret := range []string{
			"query-secret", "hunter2", "admin", "sig=", "token=", "frag", "redirect-secret", "initial-secret",
		} {
			s.NotContains(variant.failure, secret, ref)
		}
		s.NotContains(variant.failure, "\n", "diagnostics are one concise line")
	}
}

func (s *AssetDownloadSuite) TestRedactURLsInText() {
	authored := &url.URL{
		Scheme: testSchemeHTTPS, Host: "host.example", Path: "/a", RawQuery: "x=1",
		User: url.UserPassword("u", "p"),
	}
	got := redactURLsInText(`Get "` + authored.String() + `": boom; also http://h/b#f.`)

	s.NotContains(got, "u:p")
	s.NotContains(got, "x=1")
	s.Contains(got, "https://host.example/a")
	s.Contains(got, "http://h/b")
}

func (s *AssetDownloadSuite) TestDiagnosticEmittedOncePerRevision() {
	cacheDir := s.T().TempDir()
	inputs := iconInputs{icon: "https://example.invalid/logo.svg"}
	failed := iconVariant{failure: "HTTP status 404"}

	revision, created, err := storeIconRevision(cacheDir, inputs, failed, iconVariant{})
	s.Require().NoError(err)
	s.True(created)

	again, created, err := storeIconRevision(cacheDir, inputs, failed, iconVariant{})
	s.Require().NoError(err)
	s.False(created, "re-recording the same failed revision must stay silent")
	s.Equal(revision, again)
}

func (s *AssetDownloadSuite) TestLocalIconBounds() {
	dir := s.T().TempDir()
	s.Require().
		NoError(os.WriteFile(dir+"/big.svg", bytes.Repeat([]byte("a"), maxIconBytes+1), 0o600))

	_, err := readLocalIcon(dir, "big.svg")
	s.Error(err)

	_, err = readLocalIcon(dir, ".")
	s.Error(err, "directories are not images")

	_, err = readLocalIcon(dir, "absent.svg")
	s.Error(err)
}

func (s *AssetDownloadSuite) TestLocalIconAcceptsContainedAbsoluteSymlinkAndRejectsEscapeSwap() {
	const iconLink = "link.svg"
	dir := s.T().TempDir()
	manifest := filepath.Join(dir, "provider.yaml")
	s.Require().NoError(os.WriteFile(manifest, widgetManifest(""), 0o600))
	inside := filepath.Join(dir, "inside.svg")
	s.Require().NoError(os.WriteFile(inside, []byte(testIconSVGLight), 0o600))
	link := filepath.Join(dir, iconLink)
	if err := os.Symlink(inside, link); err != nil {
		s.T().Skipf("symlinks unavailable: %v", err)
	}
	_, err := ResolveProviderIconReference(ProviderSource{File: manifest}, iconLink)
	s.Require().NoError(err)
	data, err := readLocalIcon(dir, iconLink)
	s.Require().NoError(err)
	s.Equal(
		testIconSVGLight,
		string(data),
		"an absolute symlink inside the manifest directory is allowed",
	)

	outside := filepath.Join(s.T().TempDir(), "outside.svg")
	s.Require().NoError(os.WriteFile(outside, []byte(testIconSVGDark), 0o600))
	s.Require().NoError(os.Remove(link))
	s.Require().NoError(os.Symlink(outside, link))

	_, err = readLocalIcon(dir, iconLink)
	s.Require().Error(err, "a link escaping after reference resolution must not be opened")
}

func (s *AssetDownloadSuite) TestOfflineFetchFails() {
	srv := s.serve(func(http.ResponseWriter, *http.Request) {})
	target := srv.URL + "/logo.svg"
	srv.Close()

	variant := s.fetcher(nil).fetchVariant(context.Background(), ProviderSource{}, target)

	s.NotEmpty(variant.failure)
	s.Nil(variant.data)
	s.False(strings.Contains(variant.failure, "?"))
}

func (s *AssetDownloadSuite) fetcher(credentials *fakeCredentials) *iconFetcher {
	f := &iconFetcher{client: newIconHTTPClient(), timeout: 2 * time.Second}
	if credentials != nil {
		f.credentials = credentials
	}
	s.T().Cleanup(f.close)
	return f
}

func (s *AssetDownloadSuite) serve(handler http.HandlerFunc) *httptest.Server {
	srv := httptest.NewServer(handler)
	s.T().Cleanup(srv.Close)
	return srv
}

func (s *AssetDownloadSuite) widgetWithIcons(icon, iconDark string) *ProviderConfig {
	cfg, err := ParseAuthorProvider(bytes.NewReader(widgetManifest("")))
	s.Require().NoError(err)
	cfg.Icon = icon
	cfg.IconDark = iconDark
	return cfg
}

func (s *AssetDownloadSuite) downloadFailureHandler() http.HandlerFunc {
	truncatedPNG := pngBytes(s.T(), 4, 4)
	truncatedPNG = truncatedPNG[:len(truncatedPNG)-12]
	truncatedJPEG := jpegBytes(s.T(), 4, 4)
	truncatedJPEG = truncatedJPEG[:len(truncatedJPEG)-2]
	truncatedWebP := validWebPBytes(s.T())
	truncatedWebP = truncatedWebP[:len(truncatedWebP)-2]
	big := bytes.Repeat([]byte("a"), maxIconBytes+10)
	mux := http.NewServeMux()
	mux.HandleFunc("/ok.svg", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(testIconSVGLight))
	})
	mux.HandleFunc("/html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>sign in</html>"))
	})
	mux.HandleFunc("/png-as-html", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>sign in</html>"))
	})
	mux.HandleFunc("/declared-big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "99999999")
		_, _ = w.Write(big[:10])
	})
	mux.HandleFunc("/streamed-big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(big)
	})
	mux.HandleFunc("/broken.svg", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(svgOpen + "<rect>"))
	})
	mux.HandleFunc("/huge.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pngBytes(s.T(), maxIconDimension+1, 1))
	})
	for path, payload := range map[string][]byte{
		"/truncated-png":  truncatedPNG,
		"/truncated-jpeg": truncatedJPEG,
		"/truncated-webp": truncatedWebP,
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		})
	}
	mux.HandleFunc("/error", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	return mux.ServeHTTP
}
