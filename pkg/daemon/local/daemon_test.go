package local

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
)

const (
	testDefaultContext = "default"
	testProviderSSH2   = "ssh2"
	testProviderSSH    = "ssh"
	testProviderDocker = "docker"
	testDefaultJSONKey = "default"
)

func TestWithDefaults_FlagsCurrentProvider(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {DefaultProvider: testProviderSSH2},
		},
		DefaultContext: testDefaultContext,
	}

	providers := map[string]*workspace.ProviderWithOptions{
		testProviderDocker: {Config: &provider.ProviderConfig{Name: testProviderDocker}},
		testProviderSSH:    {Config: &provider.ProviderConfig{Name: testProviderSSH}},
		testProviderSSH2:   {Config: &provider.ProviderConfig{Name: testProviderSSH2}},
	}

	got := withDefaults(cfg, providers)
	if len(got) != 3 {
		t.Fatalf("got %d providers, want 3", len(got))
	}
	if !got[testProviderSSH2].Default {
		t.Errorf("%s should be marked Default", testProviderSSH2)
	}
	if got[testProviderSSH].Default {
		t.Errorf("%s should not be marked Default", testProviderSSH)
	}
	if got[testProviderDocker].Default {
		t.Errorf("%s should not be marked Default", testProviderDocker)
	}
}

func TestWithDefaults_NoDefaultProvider(t *testing.T) {
	cfg := &config.Config{
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
		DefaultContext: testDefaultContext,
	}
	providers := map[string]*workspace.ProviderWithOptions{
		testProviderDocker: {Config: &provider.ProviderConfig{Name: testProviderDocker}},
	}

	got := withDefaults(cfg, providers)
	if got[testProviderDocker].Default {
		t.Errorf("no provider should be marked Default when DefaultProvider is empty")
	}
}

func TestWithDefaults_JSONShapeHasTopLevelDefault(t *testing.T) {
	// Regression: the desktop watcher reads `entry.default` at the top level
	// of each map value. Make sure the embedded ProviderWithOptions doesn't
	// hide the Default field.
	cfg := &config.Config{
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {DefaultProvider: testProviderSSH2},
		},
		DefaultContext: testDefaultContext,
	}
	providers := map[string]*workspace.ProviderWithOptions{
		testProviderSSH2: {Config: &provider.ProviderConfig{Name: testProviderSSH2}},
	}

	raw, err := json.Marshal(withDefaults(cfg, providers))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded[testProviderSSH2][testDefaultJSONKey] != true {
		t.Errorf(
			"expected %s.%s == true in JSON output, got: %s",
			testProviderSSH2, testDefaultJSONKey, raw,
		)
	}
}
