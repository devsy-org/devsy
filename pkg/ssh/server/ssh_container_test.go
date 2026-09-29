package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadSessionSecretEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "SUPERFOO"), []byte("sentinel-value"), 0o600,
	); err != nil {
		t.Fatal("write secret fixture")
	}

	got, err := readSessionSecretEnvironment(dir)
	if err != nil {
		t.Fatal("read session secret environment")
	}
	if len(got) != 1 || got[0] != "SUPERFOO=sentinel-value" {
		t.Error("session secret environment was not reconstructed")
	}
}

func TestMergeSessionEnvironmentAttachedDefaults(t *testing.T) {
	got := mergeSessionEnvironment(
		[]string{"PATH=/usr/bin", "TOKEN=base"},
		[]string{"TOKEN=explicit"},
		[]string{"TOKEN=attached", "OTHER=attached"},
	)
	want := []string{"PATH=/usr/bin", "TOKEN=explicit", "OTHER=attached"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("merged environment does not preserve explicit values")
	}
}
