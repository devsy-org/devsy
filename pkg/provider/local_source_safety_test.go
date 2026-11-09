package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/stretchr/testify/suite"
)

type localSourceSafetySuite struct{ suite.Suite }

func TestLocalSourceSafety(t *testing.T) { suite.Run(t, new(localSourceSafetySuite)) }

func (s *localSourceSafetySuite) TestChecksumMismatch() {
	for _, tc := range []struct {
		name   string
		remote bool
	}{{name: "absolute source"}, {name: "downloaded source", remote: true}} {
		s.Run(tc.name, func() {
			dir := s.T().TempDir()
			source := filepath.Join(dir, "source")
			contents := []byte("actual binary")
			s.Require().NoError(os.WriteFile(source, contents, 0o600))
			path := source
			var requests atomic.Int32
			if tc.remote {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						_, _ = w.Write(contents)
					}),
				)
				defer server.Close()
				path = server.URL + "/binary"
			}
			expected := strings.Repeat("0", 64)
			actual := fmt.Sprintf("%x", sha256.Sum256(contents))
			binary := &ProviderBinary{
				OS:       runtime.GOOS,
				Arch:     runtime.GOARCH,
				Path:     path,
				Checksum: expected,
			}
			_, err := downloadBinaryForPlatform(
				context.Background(),
				"TEST",
				[]*ProviderBinary{binary},
				filepath.Join(dir, "storage"),
			)
			s.Require().Error(err)
			if tc.remote {
				s.Positive(requests.Load())
				s.ErrorIs(err, errChecksumVerificationFailed)
				s.NoFileExists(getBinaryPath(binary, filepath.Join(dir, "storage", "test")))
			} else {
				s.FileExists(source)
				got, readErr := hash.File(source)
				s.Require().NoError(readErr)
				s.Equal(actual, got)
				s.ErrorContains(err, source)
				s.ErrorContains(err, expected)
				s.ErrorContains(err, actual)
				s.ErrorContains(err, "regenerate")
			}
		})
	}
}

func (s *localSourceSafetySuite) TestManifestPaths() {
	dir := s.T().TempDir()
	existing := filepath.Join(dir, "manifest")
	s.Require().NoError(os.WriteFile(existing, []byte("name: test\n"), 0o600))
	for _, tc := range []struct {
		name, path string
		missing    bool
	}{
		{name: "missing yaml", path: filepath.Join(dir, "missing.yaml"), missing: true},
		{name: "missing yml", path: filepath.Join(dir, "missing.yml"), missing: true},
		{name: "missing absolute path", path: filepath.Join(dir, "missing"), missing: true},
		{name: "existing without extension", path: existing},
	} {
		s.Run(tc.name, func() {
			data, source, err := ResolveProvider(context.Background(), tc.path)
			if tc.missing {
				s.Require().Error(err)
				s.ErrorContains(err, "provider manifest file not found: "+tc.path)
				return
			}
			s.Require().NoError(err)
			s.Equal("name: test\n", string(data))
			s.Equal(tc.path, source.File)
		})
	}
}

func (s *localSourceSafetySuite) TestRelativeSourceAtDestination() {
	dir := s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(dir, "test"), 0o750))
	source := filepath.Join(dir, "test", "binary")
	s.Require().NoError(os.WriteFile(source, []byte("local"), 0o600))
	workingDir, err := os.Getwd()
	s.Require().NoError(err)
	relative, err := filepath.Rel(workingDir, source)
	s.Require().NoError(err)
	binary := &ProviderBinary{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Path:     relative,
		Checksum: strings.Repeat("0", 64),
	}
	_, err = downloadBinaryForPlatform(context.Background(), "TEST", []*ProviderBinary{binary}, dir)
	s.Require().ErrorContains(err, "regenerate")
	s.FileExists(source)
	got, readErr := hash.File(source)
	s.Require().NoError(readErr)
	s.Equal(fmt.Sprintf("%x", sha256.Sum256([]byte("local"))), got)
}

func (s *localSourceSafetySuite) TestInvalidCachedBinaryIsRemoved() {
	file := filepath.Join(s.T().TempDir(), "cached")
	s.Require().NoError(os.WriteFile(file, []byte("stale"), 0o600))
	s.False(verifyOrRemoveBinary(file, strings.Repeat("0", 64)))
	s.NoFileExists(file)
}

func (s *localSourceSafetySuite) TestGitHubSourcesDoNotResolveAsFiles() {
	for _, source := range []string{"owner/repo", "owner/repo.yaml", "github.com/owner/repo.yml"} {
		s.Run(source, func() {
			_, ok, err := resolveFileProvider(source, &ProviderSource{})
			s.NoError(err)
			s.False(ok)
		})
	}
}

func (s *localSourceSafetySuite) TestMissingAbsoluteSourceUsesStoredCopy() {
	dir := s.T().TempDir()
	target := filepath.Join(dir, "test", "binary")
	s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o750))
	content := []byte("installed binary")
	s.Require().NoError(os.WriteFile(target, content, 0o600))
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))
	binary := &ProviderBinary{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Path:     filepath.Join(dir, "missing"),
		Name:     "binary",
		Checksum: checksum,
	}
	got, err := downloadBinaryForPlatform(
		context.Background(),
		"TEST",
		[]*ProviderBinary{binary},
		dir,
	)
	s.Require().NoError(err)
	s.Equal(target, got)
	s.FileExists(target)
	config := &ProviderConfig{Binaries: map[string][]*ProviderBinary{"TEST": {binary}}}
	binaries, err := GetBinariesFrom(config, dir)
	s.Require().NoError(err)
	s.Equal(target, binaries["TEST"])
	s.Require().NoError(os.WriteFile(target, []byte("changed"), 0o600))
	_, err = GetBinariesFrom(config, dir)
	s.Require().Error(err)
	s.FileExists(target)
}

func (s *localSourceSafetySuite) TestMissingExplicitManifestForms() {
	name := filepath.Base(s.T().TempDir()) + ".yaml"
	home, err := os.UserHomeDir()
	s.Require().NoError(err)
	cases := []struct {
		name, source, want string
		local              bool
	}{
		{
			name:   "dot",
			source: "./" + name,
			want:   filepath.Join(mustWorkingDir(s), name),
			local:  true,
		},
		{
			name:   "parent",
			source: "../" + name,
			want:   filepath.Join(filepath.Dir(mustWorkingDir(s)), name),
			local:  true,
		},
		{
			name:   "absolute",
			source: filepath.Join(s.T().TempDir(), name),
			want:   filepath.Join(s.T().TempDir(), name),
			local:  true,
		},
		{name: "home", source: "~/" + name, want: filepath.Join(home, name), local: true},
		{name: "bare repository", source: "owner/repo.yaml"},
		{name: "bare yaml", source: name},
	}
	cases[2].want = cases[2].source
	if runtime.GOOS == windowsOS {
		drivePath := filepath.Join(filepath.VolumeName(mustWorkingDir(s))+`\`, name)
		cases = append(cases, struct {
			name, source, want string
			local              bool
		}{name: "backslash", source: `.\` + name, want: filepath.Join(mustWorkingDir(s), name), local: true})
		cases = append(cases, struct {
			name, source, want string
			local              bool
		}{name: "drive", source: drivePath, want: drivePath, local: true})
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, ok, err := resolveFileProvider(tc.source, &ProviderSource{})
			if !tc.local {
				s.NoError(err)
				s.False(ok)
				return
			}
			s.True(ok)
			s.Require().Error(err)
			s.ErrorContains(err, "provider manifest file not found: "+tc.want)
		})
	}
}

func mustWorkingDir(s *localSourceSafetySuite) string {
	dir, err := os.Getwd()
	s.Require().NoError(err)
	return dir
}

func (s *localSourceSafetySuite) TestExplicitNonFileAndHomePrefix() {
	dir := s.T().TempDir()
	_, ok, err := resolveFileProvider(dir, &ProviderSource{})
	s.True(ok)
	s.ErrorContains(err, "not a regular file")
	path, err := expandManifestPath("~providers/provider.yaml")
	s.NoError(err)
	s.Equal("~providers/provider.yaml", path)
}

func (s *localSourceSafetySuite) TestValidStoredCopySurvivesChangedRelativeSource() {
	dir := s.T().TempDir()
	source := filepath.Join(dir, "source")
	stored := filepath.Join(dir, "storage", "test", "binary")
	s.Require().NoError(os.MkdirAll(filepath.Dir(stored), 0o750))
	s.Require().NoError(os.WriteFile(source, []byte("changed"), 0o600))
	content := []byte("installed")
	s.Require().NoError(os.WriteFile(stored, content, 0o600))
	cwd, err := os.Getwd()
	s.Require().NoError(err)
	relative, err := filepath.Rel(cwd, source)
	s.Require().NoError(err)
	binary := &ProviderBinary{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Path:     relative,
		Name:     "binary",
		Checksum: fmt.Sprintf("%x", sha256.Sum256(content)),
	}
	got, err := downloadBinaryForPlatform(
		context.Background(),
		"TEST",
		[]*ProviderBinary{binary},
		filepath.Join(dir, "storage"),
	)
	s.Require().NoError(err)
	s.Equal(stored, got)
	s.FileExists(source)
}
