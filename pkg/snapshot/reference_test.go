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

	secureRef, err := parseReference("ghcr.io/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	notOptedInRef, err := parseReference("host.docker.internal:5000/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secureRef.Context().Scheme() != notOptedInRef.Context().Scheme() {
		t.Fatalf(
			"expected host.docker.internal ref to be secure like any other registry without %s set, got %q vs %q",
			config.EnvInsecureDockerInternal,
			notOptedInRef.Context().Scheme(),
			secureRef.Context().Scheme(),
		)
	}
}

func TestParseReference_MarksDockerInternalHostInsecureWhenOptedIn(t *testing.T) {
	t.Setenv(config.EnvInsecureDockerInternal, "true")

	secureRef, err := parseReference("ghcr.io/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	insecureRef, err := parseReference("host.docker.internal:5000/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secureRef.Context().Scheme() == insecureRef.Context().Scheme() {
		t.Fatalf(
			"expected host.docker.internal ref to use a different scheme than a normal registry, both got %q",
			secureRef.Context().Scheme(),
		)
	}
}
