package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBinaries(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range platforms {
		path := filepath.Join(dir, p.Binary)
		if err := os.WriteFile(path, []byte("content-"+p.Binary), 0o644); err != nil {
			t.Fatalf("write %s: %v", p.Binary, err)
		}
	}
	return dir
}

func TestRender(t *testing.T) {
	out, err := render(writeBinaries(t), "devsy-org/devsy", "v1.2.3")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// sha256("content-devsy-homebrew-darwin-arm64")
	const wantARM = "8d309b67abfc5861b1bfb39ea9ed2eda8622eb3aa1f82131cbbefce2bb6ffad7"

	for _, want := range []string{
		`version "1.2.3"`, // leading v stripped
		`license "MPL-2.0"`,
		"https://github.com/devsy-org/devsy/releases/download/v1.2.3/devsy-homebrew-darwin-arm64",
		"https://github.com/devsy-org/devsy/releases/download/v1.2.3/devsy-homebrew-linux-amd64",
		`sha256 "` + wantARM + `"`,
		`bin.install Dir["devsy-homebrew-*"].first => "devsy"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formula missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMissingBinary(t *testing.T) {
	dir := writeBinaries(t)
	if err := os.Remove(filepath.Join(dir, "devsy-homebrew-linux-arm64")); err != nil {
		t.Fatal(err)
	}
	if _, err := render(dir, "devsy-org/devsy", "v1.2.3"); err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}
