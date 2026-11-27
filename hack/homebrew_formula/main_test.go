package main

import (
	"fmt"
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

	// sha256("content-devsy-darwin-arm64-homebrew")
	const wantARM = "8e5144bd62aa3a04fc0db2a9ed1e29f55e8b4248fae304586f9e0a99eb3a28de"

	for _, want := range []string{
		`version "1.2.3"`, // leading v stripped
		`license "MPL-2.0"`,
		"https://github.com/devsy-org/devsy/releases/download/v1.2.3/devsy-darwin-arm64-homebrew",
		"https://github.com/devsy-org/devsy/releases/download/v1.2.3/devsy-linux-amd64-homebrew",
		`sha256 "` + wantARM + `"`,
		`bin.install Dir["devsy-*-homebrew"].first => "devsy"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formula missing %q\n---\n%s", want, out)
		}
	}

	for _, p := range platforms {
		want := fmt.Sprintf(
			`url "%s", using: :nounzip`,
			assetURL("devsy-org/devsy", "v1.2.3", p.Binary),
		)
		if !strings.Contains(out, want) {
			t.Errorf(
				"formula missing raw binary URL option for %s/%s: %q\n---\n%s",
				p.OS,
				p.Arch,
				want,
				out,
			)
		}
	}
}

// The CLI self-updater picks the first release asset whose name ends in
// <os>-<arch>, so managed assets must not end that way.
func TestManagedAssetNamesAvoidSelfUpdateSuffix(t *testing.T) {
	for _, p := range platforms {
		if !strings.HasPrefix(p.Binary, "devsy-") || !strings.HasSuffix(p.Binary, "-homebrew") {
			t.Errorf("asset %q must be named devsy-<os>-<arch>-homebrew", p.Binary)
		}
	}
}

func TestRenderMissingBinary(t *testing.T) {
	dir := writeBinaries(t)
	if err := os.Remove(filepath.Join(dir, "devsy-linux-arm64-homebrew")); err != nil {
		t.Fatal(err)
	}
	if _, err := render(dir, "devsy-org/devsy", "v1.2.3"); err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}
