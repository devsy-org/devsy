package extract

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildZip(t *testing.T, entries map[string]string) string {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}

	archivePath := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return archivePath
}

func TestUnzipFolder_ExtractsFiles(t *testing.T) {
	entries := map[string]string{
		"a.txt":        "hello",
		"dir/b.txt":    "nested contents",
		"dir/sub/c.go": "package main",
	}
	archivePath := buildZip(t, entries)
	dest := t.TempDir()

	if err := UnzipFolder(archivePath, dest); err != nil {
		t.Fatalf("UnzipFolder: %v", err)
	}

	for name, want := range entries {
		// #nosec G304 -- test-owned path in t.TempDir()
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestUnzipFolder_RejectsZipSlip(t *testing.T) {
	archivePath := buildZip(t, map[string]string{
		"../../evil.txt": "pwned",
	})
	dest := t.TempDir()

	err := UnzipFolder(archivePath, dest)
	if err == nil {
		t.Fatal("expected an error for a path-traversal entry, got nil")
	}
	if !strings.Contains(err.Error(), "invalid file path") {
		t.Errorf("error = %v, want a Zip Slip rejection", err)
	}

	escapedPath := filepath.Join(filepath.Dir(dest), "evil.txt")
	if _, statErr := os.Stat(escapedPath); !os.IsNotExist(statErr) {
		t.Error("Zip Slip entry was written outside the destination")
	}
}

func TestUnzipFolder_RejectsOversizedEntry(t *testing.T) {
	archivePath := buildZip(t, map[string]string{
		"big.bin": strings.Repeat("x", 10),
	})
	dest := t.TempDir()

	// Exercise the bound with a tiny limit so the test doesn't need to
	// build a real multi-GB archive.
	origLimit := maxUnzipEntrySize
	maxUnzipEntrySize = 5
	defer func() { maxUnzipEntrySize = origLimit }()

	err := UnzipFolder(archivePath, dest)
	if err == nil {
		t.Fatal("expected an error for an oversized entry, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds the") {
		t.Errorf("error = %v, want a size-limit rejection", err)
	}
}
