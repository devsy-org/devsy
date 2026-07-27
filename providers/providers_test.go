package providers_test

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/providers"
)

// TestBuiltInProvidersParse ensures every embedded provider.yaml parses and
// validates, guarding against malformed YAML or invalid driver config.
func TestBuiltInProvidersParse(t *testing.T) {
	for name, raw := range providers.GetBuiltInProviders() {
		t.Run(name, func(t *testing.T) {
			cfg, err := provider.ParseProvider(strings.NewReader(raw))
			if err != nil {
				t.Fatalf("parse provider %q: %v", name, err)
			}
			if cfg.Name == "" {
				t.Errorf("provider %q parsed with empty name", name)
			}
		})
	}
}

func TestAppleProviderUsesAppleDriver(t *testing.T) {
	cfg, err := provider.ParseProvider(strings.NewReader(providers.AppleProvider))
	if err != nil {
		t.Fatalf("parse apple provider: %v", err)
	}
	if cfg.Agent.Driver != provider.AppleDriver {
		t.Errorf("apple provider driver = %q, want %q", cfg.Agent.Driver, provider.AppleDriver)
	}
}

func TestMicrosandboxProviderUsesMicrosandboxDriver(t *testing.T) {
	cfg, err := provider.ParseProvider(strings.NewReader(providers.MicrosandboxProvider))
	if err != nil {
		t.Fatalf("parse microsandbox provider: %v", err)
	}
	if cfg.Agent.Driver != provider.MicrosandboxDriver {
		t.Errorf(
			"microsandbox provider driver = %q, want %q",
			cfg.Agent.Driver, provider.MicrosandboxDriver,
		)
	}
}
