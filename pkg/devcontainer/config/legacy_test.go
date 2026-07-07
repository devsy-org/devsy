package config

import (
	"slices"
	"testing"
)

const sharedExt = "shared.ext"

func vscodeOf(t *testing.T, config *DevContainerConfig) *VSCodeCustomizations {
	t.Helper()
	vscode := &VSCodeCustomizations{}
	if err := convert(config.Customizations[testUserVscode], vscode); err != nil {
		t.Fatalf("convert vscode customizations: %v", err)
	}
	return vscode
}

func TestReplaceLegacyExtensionsMergeWithNewStyle(t *testing.T) {
	config := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			Extensions: []string{"legacy.ext", sharedExt},
		},
		DevContainerActions: DevContainerActions{
			Customizations: map[string]any{
				testUserVscode: map[string]any{
					"extensions": []string{"new.ext", sharedExt},
				},
			},
		},
	}

	out, err := replaceLegacy(config)
	if err != nil {
		t.Fatal(err)
	}
	if out.Extensions != nil {
		t.Errorf("legacy Extensions should be cleared, got %v", out.Extensions)
	}

	got := vscodeOf(t, out).Extensions
	// New-style entries are preserved and legacy ones appended without dupes.
	for _, want := range []string{"new.ext", sharedExt, "legacy.ext"} {
		if !slices.Contains(got, want) {
			t.Errorf("expected %q in merged extensions, got %v", want, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("expected 3 unique extensions, got %d: %v", len(got), got)
	}
}

func TestReplaceLegacyDevPortClearedWhenNewStyleWins(t *testing.T) {
	config := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{DevPort: 8080},
		DevContainerActions: DevContainerActions{
			Customizations: map[string]any{
				testUserVscode: map[string]any{"devPort": float64(9090)},
			},
		},
	}

	out, err := replaceLegacy(config)
	if err != nil {
		t.Fatal(err)
	}
	if out.DevPort != 0 {
		t.Errorf("legacy DevPort should be cleared, got %d", out.DevPort)
	}
	if got := vscodeOf(t, out).DevPort; got != 9090 {
		t.Errorf("new-style DevPort should win, got %d", got)
	}
}

func TestReplaceLegacyDevPortBackfilled(t *testing.T) {
	config := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{DevPort: 8080},
	}

	out, err := replaceLegacy(config)
	if err != nil {
		t.Fatal(err)
	}
	if out.DevPort != 0 {
		t.Errorf("legacy DevPort should be cleared, got %d", out.DevPort)
	}
	if got := vscodeOf(t, out).DevPort; got != 8080 {
		t.Errorf("DevPort should be backfilled, got %d", got)
	}
}
