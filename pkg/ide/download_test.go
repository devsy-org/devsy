package ide

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(
		&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadAndExtractSuccess(t *testing.T) {
	archive := buildTarGz(t, "code", "#!/bin/sh\necho code\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	dest := t.TempDir()
	if err := DownloadAndExtract(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "code")); err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}
}

func TestDownloadAndExtractCorruptArchiveReportsExtractError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A complete transfer of bytes that are not a valid archive: the
		// download succeeds, so the failure must be attributed to extraction.
		_, _ = w.Write([]byte("not-a-tarball"))
	}))
	defer srv.Close()

	err := DownloadAndExtract(context.Background(), srv.URL, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "extract") {
		t.Fatalf("expected an extract-attributed error, got %v", err)
	}
}

func TestDownloadAndExtractCleansUpOnExtractFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-a-tarball"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "install")
	if err := DownloadAndExtract(context.Background(), srv.URL, dest); err == nil {
		t.Fatal("expected extract failure")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected dest removed after extract failure, stat err = %v", err)
	}
}

func TestDownloadAndExtractPreservesExistingInstallOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-a-tarball"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "install")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dest, "existing")
	if err := os.WriteFile(existing, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DownloadAndExtract(context.Background(), srv.URL, dest); err == nil {
		t.Fatal("expected extract failure")
	}

	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("expected pre-existing install to remain intact: %v", err)
	}
	if string(got) != "keep-me" {
		t.Fatalf("pre-existing content changed, got %q", got)
	}
}

func TestDownloadAndExtractReplacesExistingInstallOnSuccess(t *testing.T) {
	archive := buildTarGz(t, "code", "#!/bin/sh\necho code\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "install")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dest, "stale")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DownloadAndExtract(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "code")); err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale file replaced, stat err = %v", err)
	}
}
