package vscodeweb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"k8s.io/apimachinery/pkg/util/wait"
)

// buildCodeTarGz returns a gzipped tar holding a single `code` binary at its
// root, matching the layout of the real VS Code CLI tarball.
func buildCodeTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho code\n")
	if err := tw.WriteHeader(
		&tar.Header{Name: "code", Mode: 0o755, Size: int64(len(content))},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
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
	archive := buildCodeTarGz(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	location := t.TempDir()
	if err := downloadAndExtract(srv.URL, location); err != nil {
		t.Fatalf("downloadAndExtract: %v", err)
	}
	if _, err := os.Stat(binaryPath(location)); err != nil {
		t.Fatalf("expected extracted code binary: %v", err)
	}
}

func TestDownloadToFileTruncatedTransferReportsDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Declare more bytes than are written, then return so the connection
		// closes early — the client sees a truncated body.
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	err := downloadToFile(srv.URL, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for truncated download")
	}
	if !strings.Contains(err.Error(), "download VS Code CLI") {
		t.Fatalf("expected a download-attributed error, got %q", err)
	}
	if strings.Contains(err.Error(), "extract") {
		t.Fatalf("truncation must not be reported as an extract failure: %q", err)
	}
}

func TestDownloadToFileErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := downloadToFile(srv.URL, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected a 404-attributed error, got %v", err)
	}
}

func TestDownloadAndExtractWithRetryRecoversFromTransientFailure(t *testing.T) {
	orig := vscodeCLIBackoff
	vscodeCLIBackoff = wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Steps: 3}
	defer func() { vscodeCLIBackoff = orig }()

	archive := buildCodeTarGz(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte("short"))
			return
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	location := t.TempDir()
	if err := downloadAndExtractWithRetry(srv.URL, location); err != nil {
		t.Fatalf("expected retry to recover, got %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", calls)
	}
	if _, err := os.Stat(filepath.Join(location, "code")); err != nil {
		t.Fatalf("expected extracted binary after retry: %v", err)
	}
}

func TestGetReleaseURLDefaultVersion(t *testing.T) {
	v := NewVSCodeWeb(ServerOptions{})
	url := v.getReleaseURL()

	wantVersion := Options[VersionOption].Default
	if !strings.Contains(url, wantVersion) {
		t.Fatalf("expected url to contain default version %q, got %q", wantVersion, url)
	}
	if !strings.HasPrefix(url, "https://update.code.visualstudio.com/") {
		t.Fatalf("unexpected release host in %q", url)
	}

	wantArch := "cli-linux-x64"
	if runtime.GOARCH == archArm64 {
		wantArch = "cli-linux-arm64"
	}
	if !strings.Contains(url, wantArch) {
		t.Fatalf("expected url to contain %q, got %q", wantArch, url)
	}
}

func TestGetReleaseURLVersionOverride(t *testing.T) {
	v := NewVSCodeWeb(ServerOptions{
		Values: map[string]config.OptionValue{
			VersionOption: {Value: "1.99.0"},
		},
	})
	url := v.getReleaseURL()
	if !strings.Contains(url, "1.99.0") {
		t.Fatalf("expected url to honor VERSION override, got %q", url)
	}
}

func TestGetReleaseURLDownloadOverride(t *testing.T) {
	const custom = "https://example.test/my-vscode-cli.tar.gz"
	opt := DownloadAmd64Option
	if runtime.GOARCH == archArm64 {
		opt = DownloadArm64Option
	}
	v := NewVSCodeWeb(ServerOptions{
		Values: map[string]config.OptionValue{
			opt: {Value: custom},
		},
	})
	if got := v.getReleaseURL(); got != custom {
		t.Fatalf("expected explicit download url %q, got %q", custom, got)
	}
}

func TestIsInstalledMatchesReleaseMarker(t *testing.T) {
	location := t.TempDir()
	v := NewVSCodeWeb(ServerOptions{})
	releaseURL := v.getReleaseURL()

	if v.isInstalled(location, releaseURL) {
		t.Fatal("expected not installed when binary is missing")
	}

	if err := os.WriteFile(binaryPath(location), []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v.isInstalled(location, releaseURL) {
		t.Fatal("expected not installed when release marker is missing")
	}

	if err := writeReleaseMarker(location, releaseURL); err != nil {
		t.Fatal(err)
	}
	if !v.isInstalled(location, releaseURL) {
		t.Fatal("expected installed when binary and matching marker exist")
	}

	if v.isInstalled(location, "https://example.test/other-version") {
		t.Fatal("expected reinstall when requested release differs from marker")
	}
}

func TestWriteReleaseMarkerRoundTrip(t *testing.T) {
	location := t.TempDir()
	const url = "https://update.code.visualstudio.com/1.99.0/cli-linux-x64/stable"
	if err := writeReleaseMarker(location, url); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(releaseMarkerPath(location)) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != url {
		t.Fatalf("marker mismatch: got %q want %q", string(got), url)
	}
}

func TestNewVSCodeWebDefaults(t *testing.T) {
	v := NewVSCodeWeb(ServerOptions{})
	if v.host != "0.0.0.0" {
		t.Fatalf("expected default host 0.0.0.0, got %q", v.host)
	}
	if v.port == "" {
		t.Fatalf("expected default port to be set")
	}
}
