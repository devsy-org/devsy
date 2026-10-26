package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/suite"
)

const (
	testIconOwnerRepo     = "acme/devsy-provider-widget"
	testIconLogoPath      = "/logo.svg"
	testIconSourceRepo    = "a/b"
	testIconToken         = "tok"
	testIconAuthorization = "Bearer " + testIconToken
	testIconSVGLight      = `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8">` +
		`<rect width="8" height="8" fill="#fff"/></svg>`
	testIconSVGDark = `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8">` +
		`<rect width="8" height="8" fill="#000"/></svg>`
)

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.Gray{Y: 200})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validWebPBytes(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TBAAAAAvAAAAAAfQmf5H/wMR0f8A")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// webpExtendedBytes builds an incomplete VP8X header for dimension bounds tests.

func webpExtendedBytes(width, height uint32) []byte {
	if width == 0 || height == 0 || width > 1<<24 || height > 1<<24 {
		panic("VP8X dimensions must fit the 24-bit canvas fields")
	}
	data := make([]byte, 30)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], 22)
	copy(data[8:12], "WEBP")
	copy(data[12:16], "VP8X")
	binary.LittleEndian.PutUint32(data[16:20], 10)
	var dimension [4]byte
	binary.LittleEndian.PutUint32(dimension[:], width-1)
	copy(data[24:27], dimension[:3])
	binary.LittleEndian.PutUint32(dimension[:], height-1)
	copy(data[27:30], dimension[:3])
	return data
}

func widgetManifest(extra string) []byte {
	return []byte("name: widget\nversion: 1.0.0\nexec:\n  command: echo hi\n" + extra)
}

type AssetsSuite struct {
	suite.Suite
}

func TestAssetsSuite(t *testing.T) {
	suite.Run(t, new(AssetsSuite))
}

func (s *AssetsSuite) SetupTest() {
	setupTestHome(s.T())
}

func (s *AssetsSuite) TestResolveReferenceGithub() {
	source := ProviderSource{Github: testIconOwnerRepo, ResolvedRef: "v1.2.3"}

	location, err := ResolveProviderIconReference(source, "assets/harvester.svg")
	s.Require().NoError(err)
	s.Equal(
		githubRawBaseURL+"/"+testIconOwnerRepo+"/refs/tags/v1.2.3/assets/harvester.svg",
		location.URL,
	)
	s.Empty(location.File)

	location, err = ResolveProviderIconReference(source, "assets/logo \u00e9.svg")
	s.Require().NoError(err)
	s.True(strings.HasSuffix(location.URL, "/assets/logo%20%C3%A9.svg"), location.URL)
}

func (s *AssetsSuite) TestResolveReferenceGithubUnresolved() {
	_, err := ResolveProviderIconReference(
		ProviderSource{Github: testIconOwnerRepo, Raw: testIconOwnerRepo},
		"assets/a.svg",
	)
	s.ErrorIs(err, ErrIconSourceUnresolved)

	location, err := ResolveProviderIconReference(
		ProviderSource{Github: testIconOwnerRepo},
		"https://cdn.example.com/a.svg",
	)
	s.Require().NoError(err, "absolute icons stay eligible without provenance")
	s.Equal("https://cdn.example.com/a.svg", location.URL)
}

func (s *AssetsSuite) TestResolveReferenceURLSources() {
	tests := []struct {
		name   string
		source ProviderSource
		ref    string
		want   string
	}{
		{
			name: "direct URL is relative to the manifest URL",
			source: ProviderSource{
				URL:                 "https://host.example/path/provider.yaml",
				ResolvedManifestURL: "https://host.example/path/provider.yaml",
			},
			ref:  "assets/a.svg",
			want: "https://host.example/path/assets/a.svg",
		},
		{
			name: "raw github manifest keeps its ref and directory",
			source: ProviderSource{
				URL:                 "https://raw.githubusercontent.com/o/r/v1/sub/provider.yaml",
				ResolvedManifestURL: "https://raw.githubusercontent.com/o/r/v1/sub/provider.yaml",
			},
			ref:  "assets/a.svg",
			want: "https://raw.githubusercontent.com/o/r/v1/sub/assets/a.svg",
		},
		{
			name: "release download URL resolves to a sibling asset",
			source: ProviderSource{
				ResolvedManifestURL: "https://github.com/o/r/releases/download/v1/provider.yaml",
			},
			ref:  "a.svg",
			want: "https://github.com/o/r/releases/download/v1/a.svg",
		},
		{
			name:   "dot segments that stay inside the root are cleaned",
			source: ProviderSource{ResolvedManifestURL: "https://host.example/p/provider.yaml"},
			ref:    "./assets/../b.svg",
			want:   "https://host.example/p/b.svg",
		},
		{
			name: "escaped manifest directory preserves URL semantics",
			source: ProviderSource{
				ResolvedManifestURL: "https://host.example/dir%2Fname/provider.yaml",
			},
			ref:  "assets/logo%20%C3%A9.svg?token=icon-secret#variant",
			want: "https://host.example/dir%2Fname/assets/logo%20%C3%A9.svg?token=icon-secret#variant",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			location, err := ResolveProviderIconReference(tt.source, tt.ref)
			s.Require().NoError(err)
			s.Equal(tt.want, location.URL)
		})
	}
}

func (s *AssetsSuite) TestResolveReferenceURLAuthentication() {
	manifestURL := &url.URL{
		Scheme: testSchemeHTTPS, Host: "host.example", Path: "/p/provider.yaml",
		User: url.UserPassword("user", "pw"),
	}
	tests := []struct {
		name, query, fragment, ref, want string
	}{
		{
			name:  "manifest query secrets are not inherited",
			query: "token=secret", fragment: "frag", ref: "a.svg",
			want: "https://host.example/p/a.svg",
		},
		{
			name:  "authored query replaces rather than inherits manifest query",
			query: "token=manifest-secret", ref: "a.svg?token=icon-secret",
			want: "https://host.example/p/a.svg?token=icon-secret",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			manifestURL.RawQuery, manifestURL.Fragment = tt.query, tt.fragment
			location, err := ResolveProviderIconReference(
				ProviderSource{URL: manifestURL.String()}, tt.ref,
			)
			s.Require().NoError(err)
			s.Equal(tt.want, location.URL)
		})
	}
}

func (s *AssetsSuite) TestResolveReferenceRejectsBadReferences() {
	source := ProviderSource{
		Github:      testIconOwnerRepo,
		ResolvedRef: "v1",
	}
	bad := []string{
		"",
		"   ",
		"../x.svg",
		"assets/../../x.svg",
		"/abs.svg",
		"//evil.example/x.svg",
		`assets\x.svg`,
		"assets/%2e%2e/%2e%2e/x.svg",
		"assets%5c..%5cx.svg",
		"assets/a%00b.svg",
		"a\x00b.svg",
		"javascript:alert(1)",
		"data:image/svg+xml;base64,AAAA",
		"file:///etc/passwd",
		"ftp://host.example/a.svg",
		"https:///no-host.svg",
		".",
	}

	for _, ref := range bad {
		s.Run(ref, func() {
			_, err := ResolveProviderIconReference(source, ref)
			s.Error(err)
		})
	}
}

func (s *AssetsSuite) TestResolveReferenceBuiltin() {
	source := ProviderSource{Internal: true, Raw: DockerDriver}

	_, err := ResolveProviderIconReference(source, "assets/a.svg")
	s.Error(err)

	location, err := ResolveProviderIconReference(source, "https://cdn.example.com/docker.svg")
	s.Require().NoError(err)
	s.Equal("https://cdn.example.com/docker.svg", location.URL)
}

func (s *AssetsSuite) TestResolveReferenceLocalFile() {
	root := s.T().TempDir()
	manifest := filepath.Join(root, "provider.yaml")
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "assets"), 0o700))
	s.Require().NoError(os.WriteFile(manifest, widgetManifest(""), 0o600))
	icon := filepath.Join(root, "assets", "a.svg")
	s.Require().NoError(os.WriteFile(icon, []byte(testIconSVGLight), 0o600))
	realIcon, err := filepath.EvalSymlinks(icon)
	s.Require().NoError(err)

	location, err := ResolveProviderIconReference(ProviderSource{File: manifest}, "assets/a.svg")
	s.Require().NoError(err)
	s.Equal(IconLocation{File: realIcon}, location)

	_, err = ResolveProviderIconReference(ProviderSource{File: manifest}, "assets/missing.svg")
	s.Error(err)
}

func (s *AssetsSuite) TestResolveReferenceLocalFileRejectsSymlinkEscape() {
	root := s.T().TempDir()
	outside := s.T().TempDir()
	manifest := filepath.Join(root, "provider.yaml")
	s.Require().NoError(os.WriteFile(manifest, widgetManifest(""), 0o600))
	secret := filepath.Join(outside, "secret.svg")
	s.Require().NoError(os.WriteFile(secret, []byte(testIconSVGLight), 0o600))

	if err := os.Symlink(secret, filepath.Join(root, "file-link.svg")); err != nil {
		s.T().Skipf("symlinks unavailable: %v", err)
	}
	s.Require().NoError(os.Symlink(outside, filepath.Join(root, "dir-link")))
	s.Require().
		NoError(os.WriteFile(filepath.Join(root, "inside.svg"), []byte(testIconSVGLight), 0o600))
	s.Require().
		NoError(os.Symlink(filepath.Join(root, "inside.svg"), filepath.Join(root, "ok-link.svg")))

	source := ProviderSource{File: manifest}
	for _, ref := range []string{"file-link.svg", "dir-link/secret.svg"} {
		_, err := ResolveProviderIconReference(source, ref)
		s.Require().Error(err, ref)
	}

	_, err := ResolveProviderIconReference(source, "ok-link.svg")
	s.Require().NoError(err, "symlinks that stay inside the manifest directory are fine")
}

func (s *AssetsSuite) TestParseAuthorProviderClearsGeneratedFields() {
	manifest := widgetManifest(
		"iconRevision: " + strings.Repeat("a", 64) + "\n" +
			"source:\n  resolvedRef: v9\n  resolvedManifestURL: https://evil.example/provider.yaml\n",
	)

	authored, err := ParseAuthorProvider(bytes.NewReader(manifest))
	s.Require().NoError(err)
	s.Empty(authored.IconRevision)
	s.Empty(authored.Source.ResolvedRef)
	s.Empty(authored.Source.ResolvedManifestURL)

	trusted, err := ParseProvider(bytes.NewReader(manifest))
	s.Require().NoError(err)
	s.Equal(strings.Repeat("a", 64), trusted.IconRevision)
	s.Equal("v9", trusted.Source.ResolvedRef)
}

func (s *AssetsSuite) TestGeneratedFieldsSurviveInstalledRoundTrip() {
	cfg, err := ParseAuthorProvider(bytes.NewReader(widgetManifest("")))
	s.Require().NoError(err)
	cfg.Source = ProviderSource{
		Raw:                 testIconOwnerRepo,
		Github:              testIconOwnerRepo,
		ResolvedRef:         testTagV100,
		ResolvedManifestURL: "https://github.com/acme/r/releases/download/v1.0.0/provider.yaml",
	}
	cfg.IconRevision = strings.Repeat("b", 64)
	s.Require().NoError(SaveProviderConfig(config.DefaultContext, cfg))

	loaded, err := LoadProviderConfig(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	s.Equal(cfg.IconRevision, loaded.IconRevision)
	s.Equal(cfg.Source, loaded.Source)
}

type iconServer struct {
	*httptest.Server
	hits atomic.Int32
}

func newIconServer(s *AssetsSuite, files map[string][]byte) *iconServer {
	srv := &iconServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.hits.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	s.T().Cleanup(srv.Close)
	return srv
}

func (s *AssetsSuite) TestPrepareAndReadIcons() {
	srv := newIconServer(s, map[string][]byte{
		"/p/assets/light.svg": []byte(testIconSVGLight),
		"/p/assets/dark.png":  pngBytes(s.T(), 4, 4),
	})
	cfg := s.widget("assets/light.svg", "assets/dark.png", ProviderSource{
		URL:                 srv.URL + "/p/provider.yaml",
		ResolvedManifestURL: srv.URL + "/p/provider.yaml",
	})

	s.Require().NoError(PrepareProviderIcons(
		context.Background(), config.DefaultContext, cfg, widgetManifest(""),
	))
	s.Len(cfg.IconRevision, 64)
	s.EqualValues(2, srv.hits.Load())

	light, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.Equal(cfg.IconRevision, light.Revision)
	s.Equal(mimeSVG, light.MimeType)
	decoded, err := base64.StdEncoding.DecodeString(light.DataBase64)
	s.Require().NoError(err)
	s.Equal(testIconSVGLight, string(decoded))

	dark, err := ReadProviderIcon(config.DefaultContext, cfg, "dark")
	s.Require().NoError(err)
	s.Equal(mimePNG, dark.MimeType)

	s.EqualValues(2, srv.hits.Load(), "reads must never touch the network")

	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	entries, err := os.ReadDir(filepath.Join(dir, iconBlobsDir))
	s.Require().NoError(err)
	s.Len(entries, 2)
	for _, entry := range entries {
		info, infoErr := entry.Info()
		s.Require().NoError(infoErr)
		s.Equal(os.FileMode(0o600), info.Mode().Perm())
	}
}

func (s *AssetsSuite) TestPrepareWithoutIconsLeavesRevisionEmpty() {
	cfg := s.widget("", "", ProviderSource{})
	cfg.IconRevision = strings.Repeat("c", 64)

	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))
	s.Empty(cfg.IconRevision)

	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.Equal(&ProviderIconResult{Missing: true}, result)

	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	s.NoDirExists(dir)
}

func (s *AssetsSuite) TestFailedVariantIsRecordedAsMissing() {
	srv := newIconServer(s, map[string][]byte{"/light.svg": []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+"/light.svg", srv.URL+"/dark-404.svg", ProviderSource{})

	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))
	s.NotEmpty(cfg.IconRevision)

	light, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.False(light.Missing)

	dark, err := ReadProviderIcon(config.DefaultContext, cfg, "dark")
	s.Require().NoError(err)
	s.Equal(&ProviderIconResult{Revision: cfg.IconRevision, Missing: true}, dark)
}

func (s *AssetsSuite) TestRevisionTracksContentAndAvailability() {
	body := []byte(testIconSVGLight)
	var served atomic.Pointer[[]byte]
	served.Store(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := served.Load()
		if current == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(*current)
	}))
	s.T().Cleanup(srv.Close)

	prepare := func() string {
		cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
		s.Require().NoError(
			PrepareProviderIcons(
				context.Background(),
				config.DefaultContext,
				cfg,
				widgetManifest(""),
			),
		)
		return cfg.IconRevision
	}

	missing := prepare()
	s.Equal(missing, prepare(), "identical inputs must produce the identical revision")

	served.Store(&body)
	available := prepare()
	s.NotEqual(
		missing,
		available,
		"a previously missing image becoming available changes the revision",
	)

	changed := []byte(testIconSVGDark)
	served.Store(&changed)
	s.NotEqual(available, prepare(), "changed bytes at an unchanged URL change the revision")
}

func (s *AssetsSuite) TestRevisionTracksManifestAndSource() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	prepare := func(manifest string, source ProviderSource) string {
		cfg := s.widget(srv.URL+testIconLogoPath, "", source)
		s.Require().NoError(PrepareProviderIcons(
			context.Background(), config.DefaultContext, cfg, widgetManifest(manifest),
		))
		return cfg.IconRevision
	}

	base := prepare("", ProviderSource{Github: testIconSourceRepo, ResolvedRef: "v1"})
	s.NotEqual(
		base,
		prepare(
			"description: changed\n",
			ProviderSource{Github: testIconSourceRepo, ResolvedRef: "v1"},
		),
	)
	s.NotEqual(base, prepare("", ProviderSource{Github: testIconSourceRepo, ResolvedRef: "v2"}))
	s.NotEqual(base, prepare("", ProviderSource{Github: "c/d", ResolvedRef: "v1"}))
}

func (s *AssetsSuite) TestRelativeIconWithUnresolvedSourceIsMissing() {
	cfg := s.widget("assets/a.svg", "", ProviderSource{Github: testIconOwnerRepo})

	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.True(result.Missing)
	s.Equal(cfg.IconRevision, result.Revision)
}

func (s *AssetsSuite) TestLocalManifestIcon() {
	root := s.T().TempDir()
	manifest := filepath.Join(root, "provider.yaml")
	s.Require().NoError(os.WriteFile(manifest, widgetManifest(""), 0o600))
	s.Require().
		NoError(os.WriteFile(filepath.Join(root, "logo.svg"), []byte(testIconSVGLight), 0o600))
	cfg := s.widget("logo.svg", "", ProviderSource{File: manifest})

	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.False(result.Missing)
	s.Equal(mimeSVG, result.MimeType)
}

func (s *AssetsSuite) TestOfflinePrepareThenReadStillWorks() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	srv.Close()

	result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.False(result.Missing)

	offline := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().NoError(
		PrepareProviderIcons(context.Background(), config.DefaultContext, offline, nil),
		"an unreachable host must not fail provider setup",
	)
	missing, err := ReadProviderIcon(config.DefaultContext, offline, "light")
	s.Require().NoError(err)
	s.True(missing.Missing)
}

func (s *AssetsSuite) TestPrepareHonorsCancellation() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.Require().ErrorIs(
		PrepareProviderIcons(ctx, config.DefaultContext, cfg, nil),
		context.Canceled,
	)
	s.Empty(cfg.IconRevision)
}

func (s *AssetsSuite) TestPrepareCacheFailureLeavesRevisionEmpty() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})

	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(filepath.Dir(dir), 0o700))
	s.Require().NoError(os.WriteFile(dir, []byte("not a directory"), 0o600))

	s.Require().Error(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))
	s.Empty(cfg.IconRevision)
}

func (s *AssetsSuite) TestReadRejectsBadInputs() {
	cfg := &ProviderConfig{IconRevision: strings.Repeat("d", 64)}

	_, err := ReadProviderIcon(config.DefaultContext, cfg, "sepia")
	s.Error(err)
	_, err = ReadProviderIcon(config.DefaultContext, cfg, "")
	s.Error(err)

	for _, revision := range []string{"../../etc/passwd", "short", strings.Repeat("G", 64)} {
		_, err = ReadProviderIcon(
			config.DefaultContext,
			&ProviderConfig{IconRevision: revision},
			"light",
		)
		s.Error(err, revision)
	}
}

func (s *AssetsSuite) TestReadReportsMissingCache() {
	cfg := &ProviderConfig{Name: "widget", IconRevision: strings.Repeat("e", 64)}

	_, err := ReadProviderIcon(config.DefaultContext, cfg, "dark")
	s.Require().ErrorIs(err, os.ErrNotExist)
}

func (s *AssetsSuite) TestReadRejectsCacheNamesEscapingTheCache() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	outside := filepath.Join(s.T().TempDir(), "outside.svg")
	s.Require().NoError(os.WriteFile(outside, []byte(testIconSVGLight), 0o600))

	for _, hostile := range []string{
		"../../../../../../.." + outside,
		"../revisions/x",
		"/etc/passwd",
		iconBlobsDir + "/../../outside",
	} {
		hostileCfg := &ProviderConfig{Name: cfg.Name, IconRevision: cfg.IconRevision}
		s.tamperRevision(hostileCfg, func(m *iconRevisionMetadata) {
			m.Light.File = hostile
		})

		_, err := ReadProviderIcon(config.DefaultContext, hostileCfg, "light")
		s.Require().Error(err, hostile)
	}
}

func (s *AssetsSuite) TestReadDetectsCorruptBlob() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	entries, err := os.ReadDir(filepath.Join(dir, iconBlobsDir))
	s.Require().NoError(err)
	s.Require().Len(entries, 1)
	blob := filepath.Join(dir, iconBlobsDir, entries[0].Name())

	s.Require().NoError(os.WriteFile(blob, []byte(testIconSVGDark), 0o600))
	_, err = ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Error(err, "a blob that no longer matches its hash is a real error")

	s.Require().NoError(os.WriteFile(blob, bytes.Repeat([]byte("x"), maxIconBytes+1), 0o600))
	_, err = ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Error(err, "oversized cache entries are rejected")

	s.Require().NoError(os.Remove(blob))
	_, err = ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().ErrorIs(err, os.ErrNotExist)
}

func (s *AssetsSuite) TestReadDetectsTamperedRevisionMetadata() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))

	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	file := filepath.Join(dir, iconRevisionsDir, cfg.IconRevision+".json")
	s.Require().NoError(os.WriteFile(file, []byte(`{"schemaVersion":1}`), 0o600))

	_, err = ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Error(err)
}

func (s *AssetsSuite) TestReadReportsMissingRevisionMetadata() {
	srv := newIconServer(s, map[string][]byte{testIconLogoPath: []byte(testIconSVGLight)})
	cfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil))
	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	s.Require().NoError(os.Remove(filepath.Join(dir, iconRevisionsDir, cfg.IconRevision+".json")))

	_, err = ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().ErrorIs(err, os.ErrNotExist)
}

func (s *AssetsSuite) TestPreviousRevisionSurvivesNewRevision() {
	var body atomic.Pointer[string]
	first := testIconSVGLight
	body.Store(&first)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(*body.Load()))
	}))
	s.T().Cleanup(srv.Close)

	oldCfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().
		NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, oldCfg, nil))

	second := testIconSVGDark
	body.Store(&second)
	newCfg := s.widget(srv.URL+testIconLogoPath, "", ProviderSource{})
	s.Require().
		NoError(PrepareProviderIcons(context.Background(), config.DefaultContext, newCfg, nil))
	s.NotEqual(oldCfg.IconRevision, newCfg.IconRevision)

	for cfg, want := range map[*ProviderConfig]string{oldCfg: first, newCfg: second} {
		result, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
		s.Require().NoError(err)
		decoded, err := base64.StdEncoding.DecodeString(result.DataBase64)
		s.Require().NoError(err)
		s.Equal(want, string(decoded))
	}
}

func (s *AssetsSuite) widget(icon, iconDark string, source ProviderSource) *ProviderConfig {
	cfg, err := ParseAuthorProvider(bytes.NewReader(widgetManifest("")))
	s.Require().NoError(err)
	cfg.Icon = icon
	cfg.IconDark = iconDark
	cfg.Source = source
	return cfg
}

// tamperRevision rewrites the revision metadata and re-addresses it, so only
// the guarded fields differ from a legitimate record.
func (s *AssetsSuite) tamperRevision(
	cfg *ProviderConfig,
	mutate func(*iconRevisionMetadata),
) {
	dir, err := GetProviderAssetsDir(config.DefaultContext, cfg.Name)
	s.Require().NoError(err)
	root, err := os.OpenRoot(dir)
	s.Require().NoError(err)
	defer func() { _ = root.Close() }()

	metadata, err := loadIconRevision(root, cfg.IconRevision)
	s.Require().NoError(err)
	mutate(metadata)
	revision, err := hashIconRevision(*metadata)
	s.Require().NoError(err)

	encoded, err := json.Marshal(metadata)
	s.Require().NoError(err)
	s.Require().NoError(WriteFileAtomic(
		filepath.Join(dir, iconRevisionsDir, revision+".json"), encoded, 0o600,
	))
	cfg.IconRevision = revision
}
