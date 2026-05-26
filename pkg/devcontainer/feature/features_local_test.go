package feature

import (
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// TestProcessFeatureID_LocalPath_ResolvesRelativeToConfigOrigin verifies that
// a local feature path (e.g. "./my-feature") is resolved relative to the
// directory of the devcontainer.json file as indicated by
// DevContainerConfig.Origin, per https://containers.dev/implementors/features/.
// This is a regression test for the e2e "should install a feature into the
// container" failure where the resolver fell back to cwd because Origin was
// not propagated through MergeConfiguration.
func TestProcessFeatureID_LocalPath_ResolvesRelativeToConfigOrigin(t *testing.T) {
	originDir := filepath.Join("/abs/path", ".devcontainer")
	cfg := &config.DevContainerConfig{
		Origin: filepath.Join(originDir, "devcontainer.json"),
	}

	got, err := ProcessFeatureID("./my-feature", cfg, false)
	if err != nil {
		t.Fatalf("ProcessFeatureID: %v", err)
	}

	want, err := filepath.Abs(filepath.Join(originDir, "my-feature"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if got != want {
		t.Errorf("ProcessFeatureID(./my-feature) = %q, want %q", got, want)
	}
}

// TestProcessFeatureID_LocalPath_ViaMergedOrigin verifies that the full
// parse-then-merge-then-resolve flow used by `devsy set-up` carries Origin
// through MergeConfiguration so that local feature paths resolve correctly.
func TestProcessFeatureID_LocalPath_ViaMergedOrigin(t *testing.T) {
	originDir := filepath.Join("/abs/path", ".devcontainer")
	configPath := filepath.Join(originDir, "devcontainer.json")

	parsed := &config.DevContainerConfig{
		Origin: configPath,
	}
	parsed.Features = map[string]any{"./my-feature": map[string]any{}}

	merged, err := config.MergeConfiguration(parsed, nil)
	if err != nil {
		t.Fatalf("MergeConfiguration: %v", err)
	}
	if merged.Origin != configPath {
		t.Fatalf("merged.Origin = %q, want %q", merged.Origin, configPath)
	}

	// Mirror what cmd/setup.go resolveFeatureSets does: build a config from
	// merged fields and resolve a local feature path.
	resolveCfg := &config.DevContainerConfig{Origin: merged.Origin}
	got, err := ProcessFeatureID("./my-feature", resolveCfg, false)
	if err != nil {
		t.Fatalf("ProcessFeatureID: %v", err)
	}
	want, err := filepath.Abs(filepath.Join(originDir, "my-feature"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if got != want {
		t.Errorf("ProcessFeatureID(./my-feature) = %q, want %q", got, want)
	}
}
