package image

import (
	"strings"
	"testing"

	"github.com/docker/docker-credential-helpers/credentials"
)

func TestIsACRRegistry(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"azurecr.io suffix", "myacr.azurecr.io", true},
		{"azurecr.cn suffix", "myacr.azurecr.cn", true},
		{"azurecr.de suffix", "myacr.azurecr.de", true},
		{"azurecr.us suffix", "myacr.azurecr.us", true},
		{"mcr microsoft", "mcr.microsoft.com", true},
		{"with port", "foo.azurecr.io:443", true},
		{"docker hub", "docker.io", false},
		{"ghcr", "ghcr.io", false},
		{"bare azurecr.io apex", "azurecr.io", false},
		{"wrong tld", "myacr.azurecr.com", false},
		{"lookalike suffix", "notazurecr.io", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isACRRegistry(tc.input); got != tc.want {
				t.Errorf("isACRRegistry(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// Get must reject non-ACR registries without touching the network or Azure
// credentials, so a misrouted server URL surfaces a clear local error.
func TestACRCredHelper_GetRejectsNonACRRegistry(t *testing.T) {
	helper := newACRCredentialsHelper()
	user, pass, err := helper.Get("docker.io")
	if err == nil {
		t.Fatal("expected error for non-ACR registry, got nil")
	}
	if !strings.Contains(err.Error(), "Azure Container Registry") {
		t.Errorf("expected error to mention Azure Container Registry, got: %v", err)
	}
	if user != "" || pass != "" {
		t.Errorf("expected empty credentials, got user=%q pass=%q", user, pass)
	}
}

// Add/Delete/List are read-only-store operations ACR exchange auth does not
// support; they must report unimplemented rather than silently succeeding.
func TestACRCredHelper_UnimplementedMutations(t *testing.T) {
	helper := newACRCredentialsHelper()

	if err := helper.Add(&credentials.Credentials{}); err == nil {
		t.Fatal("expected error from unimplemented Add, got nil")
	}
	if err := helper.Delete("myacr.azurecr.io"); err == nil {
		t.Fatal("expected error from unimplemented Delete, got nil")
	}
	got, err := helper.List()
	if err == nil {
		t.Fatal("expected error from unimplemented List, got nil")
	}
	if got != nil {
		t.Errorf("expected nil map from List, got %v", got)
	}
}

// newACRCredentialsHelper returns a value satisfying credentials.Helper.
func TestNewACRCredentialsHelperSatisfiesInterface(t *testing.T) {
	helper := newACRCredentialsHelper()
	if helper == nil {
		t.Fatal("expected non-nil credentials.Helper")
	}
}
