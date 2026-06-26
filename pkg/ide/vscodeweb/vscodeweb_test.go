package vscodeweb

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

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
