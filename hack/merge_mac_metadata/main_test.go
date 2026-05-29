package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, arch, content string) {
	t.Helper()
	archDir := filepath.Join(dir, arch)
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(archDir, "latest-mac.yml"),
		[]byte(content),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

func TestMergeDedupesByURL(t *testing.T) {
	dir := t.TempDir()
	meta := filepath.Join(dir, "metadata")
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	stale := `version: 1.0.0
files:
  - url: Devsy_mac_arm64.zip
    sha512: STALE==
    size: 1
path: Devsy_mac_arm64.zip
sha512: STALE==
size: 1
`
	fresh := `version: 2.0.0
files:
  - url: Devsy_mac_arm64.zip
    sha512: FRESH==
    size: 99
  - url: Devsy_mac_x64.zip
    sha512: NEW==
    size: 88
path: Devsy_mac_arm64.zip
sha512: FRESH==
size: 99
`
	writeManifest(t, meta, "arm64", stale)
	writeManifest(t, meta, "x64", fresh)

	if err := mergeMacFiles(meta, out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "latest-mac.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "STALE==") {
		t.Fatalf("stale sha leaked through:\n%s", s)
	}
	if !strings.Contains(s, "FRESH==") || !strings.Contains(s, "NEW==") {
		t.Fatalf("missing fresh entries:\n%s", s)
	}
	// arm64.zip should appear exactly twice: once in files[], once in top-level path
	if strings.Count(s, "Devsy_mac_arm64.zip") != 2 {
		t.Fatalf("duplicate arm64 entries (expected 2 occurrences):\n%s", s)
	}
}

func setupMergeDir(t *testing.T) (meta, out string) {
	t.Helper()
	dir := t.TempDir()
	meta = filepath.Join(dir, "metadata")
	out = filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	return meta, out
}

func readMerged(t *testing.T, out string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(out, "latest-mac.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestMergeEmptyFiles(t *testing.T) {
	meta, out := setupMergeDir(t)
	content := `version: 1.0.0
files: []
`
	writeManifest(t, meta, "arm64", content)

	if err := mergeMacFiles(meta, out); err != nil {
		t.Fatal(err)
	}
	s := readMerged(t, out)
	if !strings.Contains(s, "files: []") {
		t.Fatalf("expected empty files array in output:\n%s", s)
	}
}

func TestMergeEntriesWithoutURL(t *testing.T) {
	meta, out := setupMergeDir(t)
	content := `version: 1.0.0
files:
  - sha512: NOURL==
    size: 42
`
	writeManifest(t, meta, "arm64", content)

	if err := mergeMacFiles(meta, out); err != nil {
		t.Fatal(err)
	}
	s := readMerged(t, out)
	if !strings.Contains(s, "NOURL==") {
		t.Fatalf("entry without url should still appear in output:\n%s", s)
	}
}

func TestMergeSingleSourceNoOp(t *testing.T) {
	meta, out := setupMergeDir(t)
	content := `version: 1.0.0
files:
  - url: Devsy_mac_arm64.zip
    sha512: AAA==
    size: 1
  - url: Devsy_mac_x64.zip
    sha512: BBB==
    size: 2
`
	writeManifest(t, meta, "arm64", content)

	if err := mergeMacFiles(meta, out); err != nil {
		t.Fatal(err)
	}
	s := readMerged(t, out)
	armIdx := strings.Index(s, "Devsy_mac_arm64.zip")
	x64Idx := strings.Index(s, "Devsy_mac_x64.zip")
	if armIdx < 0 || x64Idx < 0 {
		t.Fatalf("missing url entries:\n%s", s)
	}
	if armIdx > x64Idx {
		t.Fatalf("expected arm64 to precede x64 in output:\n%s", s)
	}
}
