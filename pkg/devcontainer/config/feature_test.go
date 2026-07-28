package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

const (
	depZeta  = "ghcr.io/x/zeta:1"
	depAlpha = "ghcr.io/x/alpha:1"
	depMid   = "ghcr.io/x/mid:1"
)

func TestFeatureConfig_DependsOnKeysPreservesDeclarationOrder(t *testing.T) {
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

	want := []string{depZeta, depAlpha, depMid}
	if got := cfg.DependsOnKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys() = %v, want %v", got, want)
	}
}

func TestFeatureConfig_DependsOnKeysFallsBackToSorted(t *testing.T) {
	cfg := FeatureConfig{DependsOn: DependsOnField{
		depZeta:  map[string]any{},
		depAlpha: map[string]any{},
	}}

	want := []string{depAlpha, depZeta}
	if got := cfg.DependsOnKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("DependsOnKeys() = %v, want %v", got, want)
	}
}

func TestFeatureConfig_DependsOnKeysFallsBackWhenKeyMutated(t *testing.T) {
	data := []byte(
		`{"id": "example", "dependsOn": {"ghcr.io/x/zeta:1": {}, "ghcr.io/x/alpha:1": {}}}`,
	)

	var cfg FeatureConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	delete(cfg.DependsOn, depAlpha)
	cfg.DependsOn[depMid] = map[string]any{}

	want := []string{depMid, depZeta}
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
