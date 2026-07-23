package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

func fastBackoff() DownloadOption {
	return WithBackoff(wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Steps: 3})
}

func TestDownloadToFileSuccess(t *testing.T) {
	const body = "hello-payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff()); err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	got, err := os.ReadFile(dest) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("content mismatch: got %q want %q", got, body)
	}
}

func TestDownloadToFileSendsHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Foo-Header")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(context.Background(), srv.URL, dest,
		WithHeaders(map[string]string{"Foo-Header": "Bar"}), fastBackoff())
	if err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	if gotHeader != "Bar" {
		t.Fatalf("expected header to be sent, got %q", gotHeader)
	}
}

func TestDownloadToFileTruncatedRetriesThenReportsClearError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff())
	if err == nil {
		t.Fatal("expected error for repeated truncated transfers")
	}
	if !strings.Contains(err.Error(), "download") {
		t.Fatalf("expected a download-attributed error, got %q", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts (transient), got %d", calls)
	}
}

func TestDownloadToFileRecoversAfterTransientFailure(t *testing.T) {
	const body = "recovered-payload"
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte("short"))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff()); err != nil {
		t.Fatalf("expected recovery, got %v", err)
	}
	got, err := os.ReadFile(dest) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("expected fresh content after retry, got %q", got)
	}
}

func TestDownloadToFilePermanentStatusFailsFast(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected a 404 error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 4xx to fail fast without retry, got %d attempts", calls)
	}
}

func TestDownloadToFileWithMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("binary"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "bin")
	if err := DownloadToFile(
		context.Background(),
		srv.URL,
		dest,
		WithMode(0o755),
		fastBackoff(),
	); err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("expected owner-executable file, got mode %v", info.Mode().Perm())
	}
}

func TestDownloadToFileSkipIfSameSize(t *testing.T) {
	const body = "cached-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Same length as the pre-existing file, but different content.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, strings.Repeat("x", len(body)))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := os.WriteFile(dest, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := DownloadToFile(
		context.Background(), srv.URL, dest, SkipIfSameSize(), fastBackoff(),
	); err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	got, err := os.ReadFile(dest) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("expected existing file preserved on size match, got %q", got)
	}
}

func TestDownloadToFileServerErrorRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff())
	if err == nil {
		t.Fatal("expected error for persistent 5xx")
	}
	if calls != 3 {
		t.Fatalf("expected 5xx to be retried, got %d attempts", calls)
	}
}
