package codeserver

import (
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

func TestGetReleaseURLDefaultVersion(t *testing.T) {
	c := NewCodeServer(ServerOptions{})
	url := c.getReleaseURL()

	wantVersion := Options[VersionOption].Default
	if !strings.Contains(url, wantVersion) {
		t.Fatalf("expected url to contain default version %q, got %q", wantVersion, url)
	}
	if !strings.HasPrefix(url, "https://github.com/coder/code-server/releases/download/") {
		t.Fatalf("unexpected release host in %q", url)
	}

	wantArch := "linux-amd64.tar.gz"
	if runtime.GOARCH == "arm64" {
		wantArch = "linux-arm64.tar.gz"
	}
	if !strings.Contains(url, wantArch) {
		t.Fatalf("expected url to contain %q, got %q", wantArch, url)
	}
}

func TestGetReleaseURLVersionOverride(t *testing.T) {
	c := NewCodeServer(ServerOptions{
		Values: map[string]config.OptionValue{
			VersionOption: {Value: "4.99.0"},
		},
	})
	url := c.getReleaseURL()
	if !strings.Contains(url, "4.99.0") {
		t.Fatalf("expected url to honor VERSION override, got %q", url)
	}
}

func TestGetReleaseURLDownloadOverride(t *testing.T) {
	const custom = "https://example.test/my-code-server.tar.gz"
	opt := DownloadAmd64Option
	if runtime.GOARCH == "arm64" {
		opt = DownloadArm64Option
	}
	c := NewCodeServer(ServerOptions{
		Values: map[string]config.OptionValue{
			opt: {Value: custom},
		},
	})
	if got := c.getReleaseURL(); got != custom {
		t.Fatalf("expected explicit download url %q, got %q", custom, got)
	}
}
