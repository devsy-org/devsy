package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectURLs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "latest.yml")
	writeManifest(t, dir, "latest.yml", `version: 1.0.0
files:
  - url: https://example.com/a.zip
    sha512: aa==
  - url: https://example.com/b.dmg
    sha512: bb==
path: https://example.com/a.zip
`)
	urls, err := collectURLs(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 3 {
		t.Fatalf("want 3 urls (2 files + 1 path), got %d: %v", len(urls), urls)
	}
}

func TestRunSucceedsWhenAllReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeManifest(t, dir, "latest.yml", `version: 1.0.0
files:
  - url: `+srv.URL+`/a.zip
    sha512: aa==
path: `+srv.URL+`/a.zip
`)
	if err := run(dir); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestRunFailsOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeManifest(t, dir, "latest.yml", `version: 1.0.0
files:
  - url: `+srv.URL+`/missing.zip
    sha512: aa==
path: `+srv.URL+`/missing.zip
`)
	err := run(dir)
	if err == nil {
		t.Fatal("expected failure on 404")
	}
	if !strings.Contains(err.Error(), "failed smoke test") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFailsOnRelativeURL(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "beta-mac.yml", `version: 1.0.0
files:
  - url: not-absolute.zip
    sha512: aa==
path: not-absolute.zip
`)
	err := run(dir)
	if err == nil {
		t.Fatal("expected failure on relative URL")
	}
}

func TestRunIgnoresMissingDir(t *testing.T) {
	if err := run(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("missing dir should warn, not error: %v", err)
	}
}
