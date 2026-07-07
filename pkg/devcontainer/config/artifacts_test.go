package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBuildArtifactExcludesIncludesFeatureFolder(t *testing.T) {
	got := BuildArtifactExcludes()
	if !slices.Contains(got, DevsyContextFeatureFolder) {
		t.Errorf("expected %q in excludes, got %v", DevsyContextFeatureFolder, got)
	}
}

func TestBuildArtifactExcludesReturnsCopy(t *testing.T) {
	got := BuildArtifactExcludes()
	if len(got) == 0 {
		t.Fatal("expected at least one artifact")
	}
	got[0] = "mutated"
	if BuildArtifactExcludes()[0] == "mutated" {
		t.Error("BuildArtifactExcludes must return a defensive copy")
	}
}

func TestRemoveBuildArtifacts(t *testing.T) {
	contextPath := t.TempDir()
	artifactDir := filepath.Join(contextPath, DevsyContextFeatureFolder)
	// #nosec G301 -- test fixture
	if err := os.MkdirAll(filepath.Join(artifactDir, "0"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(contextPath, "keep.txt")
	// #nosec G306 -- test fixture
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	RemoveBuildArtifacts(contextPath)

	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Errorf("expected artifact dir removed, stat err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("workspace file should survive, got %v", err)
	}
}

func TestRemoveBuildArtifactsAbsentIsNoError(t *testing.T) {
	RemoveBuildArtifacts(t.TempDir())
}
