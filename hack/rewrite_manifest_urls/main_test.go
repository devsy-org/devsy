package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteRewritesRelativeURLs(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "beta-mac.yml")
	yaml := `version: 1.10.0-beta.12
files:
  - url: Devsy_mac_arm64.zip
    sha512: abc==
    size: 100
  - url: Devsy_mac_x64.dmg
    sha512: def==
    size: 200
path: Devsy_mac_arm64.zip
sha512: abc==
size: 100
`
	if err := os.WriteFile(in, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteFile(in, "devsy-ai/devsy", "v1.10.0-beta.12"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(in)
	want := "https://github.com/devsy-ai/devsy/releases/download/v1.10.0-beta.12/Devsy_mac_arm64.zip"
	if !strings.Contains(string(got), want) {
		t.Fatalf("missing rewritten url. got:\n%s", got)
	}
	if !strings.Contains(string(got), "Devsy_mac_x64.dmg") {
		t.Fatal("second url missing")
	}
}

func TestRewriteSkipsAbsoluteURLs(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "latest.yml")
	yaml := `version: 1.0.0
files:
  - url: https://example.com/already-absolute.exe
    sha512: zz==
    size: 1
path: https://example.com/already-absolute.exe
`
	if err := os.WriteFile(in, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteFile(in, "o/r", "v1"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(in)
	if strings.Contains(string(got), "github.com/o/r") {
		t.Fatalf("should not rewrite absolute url. got:\n%s", got)
	}
}

func TestRewriteSkipsNonMapFilesEntries(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "latest.yml")
	yaml := `version: 1.0.0
files:
  - scalar1
  - scalar2
`
	if err := os.WriteFile(in, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteFile(in, "o/r", "v1"); err != nil {
		t.Fatalf("rewriteFile should not error on non-map files entries: %v", err)
	}
	got, _ := os.ReadFile(in)
	if !strings.Contains(string(got), "scalar1") || !strings.Contains(string(got), "scalar2") {
		t.Fatalf("scalar entries should round-trip unchanged. got:\n%s", got)
	}
	if strings.Contains(string(got), "github.com/o/r") {
		t.Fatalf("non-map entries should not be rewritten. got:\n%s", got)
	}
}

func TestWalkAndRewriteFilenameFilter(t *testing.T) {
	dir := t.TempDir()
	contents := `version: 1.0.0
files:
  - url: Devsy.zip
    sha512: aa==
    size: 1
path: Devsy.zip
`
	names := []string{"latest.yml", "latest-mac.yml", "beta.yml", "random.yml", "latest.yaml"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := walkAndRewrite(dir, "o/r", "v1"); err != nil {
		t.Fatal(err)
	}

	rewritten := []string{"latest.yml", "latest-mac.yml", "beta.yml"}
	for _, n := range rewritten {
		b, _ := os.ReadFile(filepath.Join(dir, n))
		if !strings.Contains(string(b), "github.com/o/r") {
			t.Fatalf("%s should have been rewritten. got:\n%s", n, b)
		}
	}

	untouched := []string{"random.yml", "latest.yaml"}
	for _, n := range untouched {
		b, _ := os.ReadFile(filepath.Join(dir, n))
		if strings.Contains(string(b), "github.com/o/r") {
			t.Fatalf("%s should NOT have been rewritten. got:\n%s", n, b)
		}
	}
}
