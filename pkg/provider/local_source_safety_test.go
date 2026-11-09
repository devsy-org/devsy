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
	"testing"

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
			if tc.remote {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
				s.NoFileExists(filepath.Join(dir, "storage", "test", "binary"))
			} else {
				s.FileExists(source)
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
}

func (s *localSourceSafetySuite) TestInvalidCachedBinaryIsRemoved() {
	file := filepath.Join(s.T().TempDir(), "cached")
	s.Require().NoError(os.WriteFile(file, []byte("stale"), 0o600))
	s.False(verifyOrRemoveBinary(file, strings.Repeat("0", 64)))
	s.NoFileExists(file)
}

func (s *localSourceSafetySuite) TestBareNameDoesNotReadLocalFile() {
	file, err := os.CreateTemp(".", "provider-name-")
	s.Require().NoError(err)
	s.Require().NoError(file.Close())
	s.T().Cleanup(func() { _ = os.Remove(file.Name()) })
	_, ok, err := resolveFileProvider(filepath.Base(file.Name()), &ProviderSource{})
	s.NoError(err)
	s.False(ok)
}

func (s *localSourceSafetySuite) TestDirectoryIsNotBinary() {
	dir := s.T().TempDir()
	_, err := verifyLocalBinary(dir, "")
	s.Require().Error(err)
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

func (s *localSourceSafetySuite) TestGitHubSourceDoesNotReadLocalFile() {
	dir, err := os.MkdirTemp(".", "owner-")
	s.Require().NoError(err)
	file := filepath.Join(dir, "repo")
	s.Require().NoError(os.WriteFile(file, []byte("local"), 0o600))
	s.T().Cleanup(func() { _ = os.Remove(file); _ = os.Remove(dir) })
	source := filepath.Base(dir) + "/repo"
	_, ok, err := resolveFileProvider(source, &ProviderSource{})
	s.NoError(err)
	s.False(ok)
}

func (s *localSourceSafetySuite) TestRelativeYAMLManifest() {
	dir, err := os.MkdirTemp(".", "manifest-dir-")
	s.Require().NoError(err)
	file := filepath.Join(dir, "provider.yaml")
	s.Require().NoError(os.WriteFile(file, []byte("name: local\n"), 0o600))
	s.T().Cleanup(func() { _ = os.Remove(file); _ = os.Remove(dir) })
	source := filepath.Base(dir) + "/provider.yaml"
	data, ok, err := resolveFileProvider(source, &ProviderSource{})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal("name: local\n", string(data))
}
