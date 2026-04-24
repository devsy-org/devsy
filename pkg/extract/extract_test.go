package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name       string
	body       string
	linkTarget string
	symlink    bool
}

func (e tarEntry) header() *tar.Header {
	if e.linkTarget != "" && e.symlink {
		return &tar.Header{
			Typeflag: tar.TypeSymlink,
			Name:     e.name,
			Linkname: e.linkTarget,
		}
	}
	if e.linkTarget != "" {
		return &tar.Header{
			Typeflag: tar.TypeLink,
			Name:     e.name,
			Linkname: e.linkTarget,
		}
	}
	return &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     e.name,
		Size:     int64(len(e.body)),
		Mode:     0o644,
	}
}

// newTarGz creates an in-memory tar.gz from a list of entries.
func newTarGz(t *testing.T, entries []tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, e := range entries {
		writeTarEntry(t, tw, e)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func writeTarEntry(t *testing.T, tw *tar.Writer, e tarEntry) {
	t.Helper()
	if err := tw.WriteHeader(e.header()); err != nil {
		t.Fatal(err)
	}
	if e.body != "" {
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtract_NormalArchive(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{name: "hello.txt", body: "world"},
	})

	dest := t.TempDir()
	if err := Extract(buf, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := filepath.Join(dest, "hello.txt")
	content, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(content) != "world" {
		t.Fatalf("got %q, want %q", string(content), "world")
	}
}

func TestExtract_PathTraversalBlocked(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{name: "../../etc/passwd", body: "malicious"},
	})

	dest := t.TempDir()
	err := Extract(buf, dest)
	if err == nil {
		t.Fatal("expected path traversal error, got nil")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("error %q does not mention path traversal", err)
	}
}

func TestExtract_SymlinkTraversalBlocked(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{
			name:       "evil-link",
			linkTarget: "../../etc/passwd",
			symlink:    true,
		},
	})

	dest := t.TempDir()
	err := Extract(buf, dest)
	if err == nil {
		t.Fatal("expected symlink traversal error, got nil")
	}
	if !strings.Contains(err.Error(), "symlink traversal") {
		t.Fatalf("error %q does not mention symlink traversal", err)
	}
}

func TestExtract_HardLinkTraversalBlocked(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{
			name:       "evil-link",
			linkTarget: "../../etc/passwd",
			symlink:    false,
		},
	})

	dest := t.TempDir()
	err := Extract(buf, dest)
	if err == nil {
		t.Fatal("expected hard link traversal error, got nil")
	}
	if !strings.Contains(err.Error(), "hard link traversal") {
		t.Fatalf("error %q doesn't mention hard link traversal", err)
	}
}

func TestExtract_ValidSymlinkAllowed(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{name: "target.txt", body: "content"},
		{
			name:       "link.txt",
			linkTarget: "target.txt",
			symlink:    true,
		},
	})

	dest := t.TempDir()
	if err := Extract(buf, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linkPath := filepath.Join(dest, "link.txt")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("symlink target = %q, want %q", target, "target.txt")
	}
}
