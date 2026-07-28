package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFeatureConfig_DependsOnKeysPreservesDeclarationOrder(t *testing.T) {
	// Keys are intentionally not alphabetical to prove declaration order is
	// preserved rather than sorted, matching the reference devcontainer CLI.
	data := []byte(`{
		"id": "example",
		"dependsOn": {
			"ghcr.io/x/zeta:1": {},
			"ghcr.io/x/alpha:1": {},
			"ghcr.io/x/mid:1": {}
		}
	}`)

	var cfg FeatureConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := []string{"ghcr.io/x/zeta:1", "ghcr.io/x/alpha:1", "ghcr.io/x/mid:1"}
	if got := cfg.DependsOnKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys() = %v, want %v", got, want)
	}
}

func TestFeatureConfig_DependsOnKeysFallsBackToSorted(t *testing.T) {
	// A programmatically constructed config has no captured order; keys must be
	// returned deterministically (sorted).
	cfg := FeatureConfig{DependsOn: DependsOnField{
		"ghcr.io/x/zeta:1":  map[string]any{},
		"ghcr.io/x/alpha:1": map[string]any{},
	}}

	want := []string{"ghcr.io/x/alpha:1", "ghcr.io/x/zeta:1"}
	if got := cfg.DependsOnKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys() = %v, want %v", got, want)
	}
}

func TestFeatureConfig_DependsOnKeysEmpty(t *testing.T) {
	var cfg FeatureConfig
	if got := cfg.DependsOnKeys(); len(got) != 0 {
		t.Errorf("DependsOnKeys() = %v, want empty", got)
	}
}
