package runenv

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type DependenciesSuite struct{ suite.Suite }

func TestDependenciesSuite(t *testing.T) { suite.Run(t, new(DependenciesSuite)) }

func (s *DependenciesSuite) TestOSFileStoreAtomicWriteAndAppend() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "state.json")
	var files OsFileStore

	s.Require().NoError(files.WriteFile(path, []byte("one"), 0o600))
	s.Require().NoError(files.WriteFile(path, []byte("two"), 0o600))
	got, err := files.ReadFile(path)
	s.Require().NoError(err)
	s.Equal("two", string(got))

	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Len(entries, 1, "no temp file left behind")

	s.Require().NoError(files.AppendFile(path, []byte("+three"), 0o600))
	got, _ = files.ReadFile(path)
	s.Equal("two+three", string(got))

	s.Require().NoError(files.Remove(path))
	_, err = files.Stat(path)
	s.Require().ErrorIs(err, os.ErrNotExist)
}

func (s *DependenciesSuite) TestOSFileStoreWriteFailureLeavesNoTemp() {
	dir := s.T().TempDir()
	var files OsFileStore
	err := files.WriteFile(filepath.Join(dir, "missing", "f"), []byte("x"), 0o600)
	s.Require().Error(err)
}

func (s *DependenciesSuite) TestRealClockSleepHonorsContext() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Require().ErrorIs(realClock{}.Sleep(ctx, time.Minute), context.Canceled)
	s.Require().NoError(realClock{}.Sleep(context.Background(), time.Millisecond))
}

func (s *DependenciesSuite) TestDownloader() {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("0123456789"))
		case "/missing":
			http.NotFound(w, r)
		}
	}))
	s.T().Cleanup(srv.Close)
	d := httpsDownloader{client: srv.Client()}

	var buf bytes.Buffer
	s.Require().NoError(d.Download(context.Background(), srv.URL+"/ok", &buf, 10))
	s.Equal("0123456789", buf.String())

	err := d.Download(context.Background(), srv.URL+"/ok", &bytes.Buffer{}, 9)
	s.Require().ErrorContains(err, "exceeds 9 bytes")

	err = d.Download(context.Background(), srv.URL+"/missing", &bytes.Buffer{}, 10)
	s.Require().ErrorContains(err, "HTTP 404")

	err = d.Download(
		context.Background(),
		strings.Replace(srv.URL, "https", "http", 1),
		&bytes.Buffer{},
		1,
	)
	s.Require().ErrorContains(err, "HTTPS")
	s.Require().ErrorContains(
		d.Download(context.Background(), "/relative", &bytes.Buffer{}, 1), "HTTPS")
}
