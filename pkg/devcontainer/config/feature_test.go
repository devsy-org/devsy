package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestParseDevContainerFeatureRequiresID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing id", `{"version":"1.0.0","name":"Go"}`},
		{"empty id", `{"id":"","version":"1.0.0","name":"Go"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(dir, DevContainerFeatureFileName), []byte(tc.raw), 0o600,
			); err != nil {
				t.Fatalf("write feature file: %v", err)
			}
			if _, err := ParseDevContainerFeature(dir); err == nil {
				t.Fatalf("ParseDevContainerFeature expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseDevContainerFeatureAcceptsID(t *testing.T) {
	dir := t.TempDir()
	raw := `{"id":"go","version":"1.0.0","name":"Go"}`
	if err := os.WriteFile(
		filepath.Join(dir, DevContainerFeatureFileName), []byte(raw), 0o600,
	); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	cfg, err := ParseDevContainerFeature(dir)
	if err != nil {
		t.Fatalf("ParseDevContainerFeature: %v", err)
	}
	if cfg.ID != "go" {
		t.Errorf("ID = %q, want %q", cfg.ID, "go")
	}
}
