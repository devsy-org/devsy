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
		if err := os.WriteFile(filepath.Join(dir, p.Binary), []byte("content-"+p.Binary), 0o644); err != nil {
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

	// sha256("content-devsy-darwin-arm64")
	const wantARM = "9bc996e636ac5321e2aae6bd3fb421c5ea82b34a5b20e2c9fd65cc81b2f3753e"

	for _, want := range []string{
		`version "1.2.3"`, // leading v stripped
		`license "MPL-2.0"`,
		"https://github.com/devsy-org/devsy/releases/download/v1.2.3/devsy-darwin-arm64",
		"https://github.com/devsy-org/devsy/releases/download/v1.2.3/devsy-linux-amd64",
		`sha256 "` + wantARM + `"`,
		`bin.install Dir["devsy-*"].first => "devsy"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formula missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMissingBinary(t *testing.T) {
	dir := writeBinaries(t)
	if err := os.Remove(filepath.Join(dir, "devsy-linux-arm64")); err != nil {
		t.Fatal(err)
	}
	if _, err := render(dir, "devsy-org/devsy", "v1.2.3"); err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}
