package http

import (
	"context"
	"errors"
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

func TestDownloadToFileLeavesNoPartialFileOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff()); err == nil {
		t.Fatal("expected error for repeated truncated transfers")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no partial file at destination, stat err = %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no leftover temp files, found %d entries", len(entries))
	}
}

func TestDownloadToFilePreservesExistingFileOnFailure(t *testing.T) {
	const existing = "previous-good-content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := os.WriteFile(dest, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff()); err == nil {
		t.Fatal("expected error for repeated truncated transfers")
	}
	got, err := os.ReadFile(dest) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatalf("expected existing file preserved, got %v", err)
	}
	if string(got) != existing {
		t.Fatalf("existing file was clobbered, got %q", got)
	}
}

func TestDownloadToFileStallTimeoutAborts(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("expected flusher")
			return
		}
		_, _ = w.Write([]byte("partial"))
		flusher.Flush()
		// Send headers and a little data, then hang: the stall watchdog must
		// abort the attempt rather than block forever.
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(
		context.Background(), srv.URL, dest,
		WithStallTimeout(50*time.Millisecond), fastBackoff(),
	)
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("expected a stalled error, got %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("expected no partial file after stall, stat err = %v", statErr)
	}
}

func TestDownloadToFileReportsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	// Backoff longer than the context deadline, so cancellation happens during
	// the wait after a transient (non-context) failure.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	slow := WithBackoff(wait.Backoff{Duration: 100 * time.Millisecond, Factor: 1, Steps: 3})

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(ctx, srv.URL, dest, slow)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline error, got %v", err)
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

func TestDownloadToFileTooManyRequestsRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := DownloadToFile(context.Background(), srv.URL, dest, fastBackoff())
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected a 429 error, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 429 to be retried, got %d attempts", calls)
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
