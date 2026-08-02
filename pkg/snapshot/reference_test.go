package snapshot

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

func TestIsDockerInternalHost(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"bare host", "host.docker.internal", true},
		{"host with port", "host.docker.internal:5000", true},
		{"repository path", "host.docker.internal:5000/acme/repo", true},
		{"full tag ref", "host.docker.internal:5000/acme/repo:tag", true},
		{"digest ref", "host.docker.internal/acme/repo@sha256:" + zeroDigest, true},
		{"other host", "ghcr.io/acme/repo:tag", false},
		{"subdomain lookalike", "notthehost.docker.internal:5000/acme/repo", false},
		{"suffix lookalike", "host.docker.internal.evil.com:5000/acme/repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDockerInternalHost(tt.in)
			if got != tt.want {
				t.Errorf("isDockerInternalHost(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseReference_TreatsDockerInternalHostAsSecureByDefault(t *testing.T) {
	t.Setenv(config.EnvInsecureDockerInternal, "")

	ref, err := parseReference("host.docker.internal:5000/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ref.Context().Scheme(); got != "https" {
		t.Fatalf("expected https without %s set, got %q", config.EnvInsecureDockerInternal, got)
	}
}

func TestParseReference_TreatsDockerInternalHostAsSecureWhenExplicitlyFalse(t *testing.T) {
	t.Setenv(config.EnvInsecureDockerInternal, "false")

	ref, err := parseReference("host.docker.internal:5000/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ref.Context().Scheme(); got != "https" {
		t.Fatalf(
			"expected https with %s=false, got %q", config.EnvInsecureDockerInternal, got,
		)
	}
}

func TestParseReference_MarksDockerInternalHostInsecureWhenOptedIn(t *testing.T) {
	t.Setenv(config.EnvInsecureDockerInternal, "true")

	ref, err := parseReference("host.docker.internal:5000/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ref.Context().Scheme(); got != "http" {
		t.Fatalf("expected http with %s=true, got %q", config.EnvInsecureDockerInternal, got)
	}
}
