package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/suite"
)

type DockerDownloadSuite struct {
	suite.Suite
	files *wslFiles
	deps  *Dependencies
	dl    *stubDownloader
}

func TestDockerDownloadSuite(t *testing.T) { suite.Run(t, new(DockerDownloadSuite)) }

func (s *DockerDownloadSuite) SetupTest() {
	s.files = newWSLFiles()
	s.dl = &stubDownloader{}
	s.deps = &Dependencies{Files: s.files, HTTP: s.dl}
}

func (s *DockerDownloadSuite) Fetch(data []byte) (string, error) {
	s.dl.data = data
	src := dockerCLISource{URL: testZipURL, SHA256: sha256Hex(data)}
	return fetchDockerCLI(context.Background(), s.deps, src, "/dest")
}

func (s *DockerDownloadSuite) TestPinnedSourceMatchesReviewedRelease() {
	s.Equal("https://download.docker.com/win/static/stable/x86_64/docker-29.9.0.zip",
		pinnedDockerCLI.URL)
	s.Equal("e3ff5d2e4cd1ad7e1c9bcb76f0df28a84853c43416a915323f08659d8487ad83",
		pinnedDockerCLI.SHA256)
}

func (s *DockerDownloadSuite) TestVerifiedArchiveExtractsAndReturnsBinDir() {
	bin, err := s.Fetch(validCLIZip(s.T()))
	s.Require().NoError(err)
	s.Equal("/dest/docker", bin)
	s.Equal("MZ-docker", s.files.text("/dest/docker/docker.exe"))
	s.Equal("MZ-dockerd", s.files.text("/dest/docker/dockerd.exe"))
}

func (s *DockerDownloadSuite) TestHashMismatchWritesNothing() {
	s.dl.data = validCLIZip(s.T())
	src := dockerCLISource{URL: testZipURL, SHA256: sha256Hex([]byte("other"))}

	_, err := fetchDockerCLI(context.Background(), s.deps, src, "/dest")

	s.Equal([]Code{CodeDockerCLIChecksum}, codesOf(err))
	s.Empty(s.files.files, "an unverified archive must never reach disk")
}

func (s *DockerDownloadSuite) TestDownloadFailures() {
	for name, tc := range map[string]struct {
		err     error
		partial bool
	}{
		"http 404":         {err: errors.New("download: HTTP 404")},
		"timeout":          {err: context.DeadlineExceeded},
		"partial download": {err: errors.New("unexpected EOF"), partial: true},
	} {
		s.Run(name, func() {
			s.files = newWSLFiles()
			s.deps.Files = s.files
			s.dl.data, s.dl.err, s.dl.partial = validCLIZip(s.T()), tc.err, tc.partial
			src := dockerCLISource{URL: testZipURL, SHA256: sha256Hex(s.dl.data)}

			_, err := fetchDockerCLI(context.Background(), s.deps, src, "/dest")

			s.Equal([]Code{CodeDockerWSLInstall}, codesOf(err))
			s.Empty(s.files.files)
		})
	}
}

func (s *DockerDownloadSuite) TestTruncatedArchiveWithMatchingHashFails() {
	data := validCLIZip(s.T())
	_, err := s.Fetch(data[:len(data)/2])
	s.Require().Error(err)
	s.Empty(s.files.files)
}

func (s *DockerDownloadSuite) TestUnsafeArchivesAreRejectedBeforeAnyWrite() {
	for name, entries := range map[string]map[string]string{
		"traversal":           {testExeEntry: "x", "../evil.exe": "x"},
		"nested traversal":    {testExeEntry: "x", "a/../../evil": "x"},
		"absolute":            {testExeEntry: "x", etcPasswd: "x"},
		"drive letter":        {testExeEntry: "x", `C:/Windows/evil.exe`: "x"},
		"backslash":           {testExeEntry: "x", `..\evil.exe`: "x"},
		"alternate stream":    {testExeEntry: "x", "docker/a.txt:stream": "x"},
		"case duplicate":      {testExeEntry: "x", "DOCKER/Docker.exe": "y"},
		"file and directory":  {testExeEntry: "x", "docker/docker.exe/inner": "y"},
		"no docker.exe":       {"docker/dockerd.exe": "x"},
		"two docker.exe":      {"a/docker.exe": "x", "b/docker.exe": "y"},
		"non canonical":       {testExeEntry: "x", "docker//a": "y"},
		"current dir segment": {testExeEntry: "x", "./a": "y"},
	} {
		s.Run(name, func() {
			s.files = newWSLFiles()
			s.deps.Files = s.files

			_, err := s.Fetch(zipOf(s.T(), entries))

			s.Equal([]Code{CodeDockerWSLInstall}, codesOf(err))
			s.Empty(s.files.files, "validation must precede the first write")
		})
	}
}

func (s *DockerDownloadSuite) TestSymlinkEntryRejected() {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: testExeEntry}
	hdr.SetMode(fs.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	s.Require().NoError(err)
	_, _ = w.Write([]byte(etcPasswd))
	s.Require().NoError(zw.Close())

	_, err = s.Fetch(buf.Bytes())

	s.Require().Error(err)
	s.Contains(err.Error(), "not a regular file")
	s.Empty(s.files.files)
}

func (s *DockerDownloadSuite) TestTooManyEntriesRejected() {
	entries := map[string]string{testExeEntry: "x"}
	for i := range maxZipEntries {
		entries[fmt.Sprintf("docker/f%d", i)] = ""
	}
	_, err := s.Fetch(zipOf(s.T(), entries))
	s.Require().Error(err)
	s.Contains(err.Error(), "entries")
}
