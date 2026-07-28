package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDMGs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range arches {
		path := filepath.Join(dir, a.DMG)
		if err := os.WriteFile(path, []byte("content-"+a.DMG), 0o644); err != nil {
			t.Fatalf("write %s: %v", a.DMG, err)
		}
	}
	return dir
}

func TestRender(t *testing.T) {
	out, err := render(writeDMGs(t), "devsy-org/devsy", "v1.2.3")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// sha256("content-Devsy_mac_arm64.dmg")
	const wantARM = "f65094affc51b44bb65e948773afbdb527e5073f82569bcd9361deb17581807b"

	for _, want := range []string{
		`version "1.2.3"`, // leading v stripped
		`cask "devsy" do`,
		`arch arm: "arm64", intel: "x64"`,
		"https://github.com/devsy-org/devsy/releases/download/v#{version}/Devsy_mac_#{arch}.dmg",
		`app "Devsy.app"`,
		`homepage "https://www.devsy.sh"`,
		`sha256 arm:   "` + wantARM + `"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cask missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMissingDMG(t *testing.T) {
	dir := writeDMGs(t)
	if err := os.Remove(filepath.Join(dir, "Devsy_mac_x64.dmg")); err != nil {
		t.Fatal(err)
	}
	if _, err := render(dir, "devsy-org/devsy", "v1.2.3"); err == nil {
		t.Fatal("expected error for missing dmg, got nil")
	}
}
