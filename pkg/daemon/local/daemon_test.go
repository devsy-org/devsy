package local

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
)

func TestWithDefaults_FlagsCurrentProvider(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.ContextConfig{
			"default": {DefaultProvider: "ssh2"},
		},
		DefaultContext: "default",
	}

	providers := map[string]*workspace.ProviderWithOptions{
		"docker": {Config: &provider.ProviderConfig{Name: "docker"}},
		"ssh":    {Config: &provider.ProviderConfig{Name: "ssh"}},
		"ssh2":   {Config: &provider.ProviderConfig{Name: "ssh2"}},
	}

	got := withDefaults(cfg, providers)
	if len(got) != 3 {
		t.Fatalf("got %d providers, want 3", len(got))
	}
	if !got["ssh2"].Default {
		t.Errorf("ssh2 should be marked Default")
	}
	if got["ssh"].Default {
		t.Errorf("ssh should not be marked Default")
	}
	if got["docker"].Default {
		t.Errorf("docker should not be marked Default")
	}
}

func TestWithDefaults_NoDefaultProvider(t *testing.T) {
	cfg := &config.Config{
		Contexts:       map[string]*config.ContextConfig{"default": {}},
		DefaultContext: "default",
	}
	providers := map[string]*workspace.ProviderWithOptions{
		"docker": {Config: &provider.ProviderConfig{Name: "docker"}},
	}

	got := withDefaults(cfg, providers)
	if got["docker"].Default {
		t.Errorf("no provider should be marked Default when DefaultProvider is empty")
	}
}

func TestWithDefaults_JSONShapeHasTopLevelDefault(t *testing.T) {
	// Regression: the desktop watcher reads `entry.default` at the top level
	// of each map value. Make sure the embedded ProviderWithOptions doesn't
	// hide the Default field.
	cfg := &config.Config{
		Contexts:       map[string]*config.ContextConfig{"default": {DefaultProvider: "ssh2"}},
		DefaultContext: "default",
	}
	providers := map[string]*workspace.ProviderWithOptions{
		"ssh2": {Config: &provider.ProviderConfig{Name: "ssh2"}},
	}

	raw, err := json.Marshal(withDefaults(cfg, providers))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["ssh2"]["default"] != true {
		t.Errorf("expected ssh2.default == true in JSON output, got: %s", raw)
	}
}
