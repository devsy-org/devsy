package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type tarEntry struct {
	name       string
	body       string
	linkTarget string
	symlink    bool
	uid        int
	gid        int
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
		Uid:      e.uid,
		Gid:      e.gid,
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
		{name: etcPasswd, body: "malicious"},
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
			linkTarget: etcPasswd,
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
			linkTarget: etcPasswd,
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

func TestExtract_HardLinkResolvesRelativeToDestinationNotCWD(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	sentinel := filepath.Join(cwd, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}

	buf := newTarGz(t, []tarEntry{
		{name: "evil-link", linkTarget: "sentinel.txt"},
	})

	dest := t.TempDir()
	if err := Extract(buf, dest); err == nil {
		t.Fatal("expected error: link target does not exist under dest")
	}

	info, err := os.Lstat(sentinel)
	if err != nil {
		t.Fatalf("sentinel disappeared: %v", err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink != 1 {
		t.Fatalf(
			"sentinel gained a hard link (Nlink=%d): extraction escaped destFolder",
			stat.Nlink,
		)
	}
}

func TestExtract_HardLinkResolvesFromArchiveRootWhenNested(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{name: testHardLinkRel, body: "world"},
		{name: "dir/sub/link", linkTarget: testHardLinkRel},
	})

	dest := t.TempDir()
	if err := Extract(buf, dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linkPath := filepath.Join(dest, "dir", "sub", "link")
	content, err := os.ReadFile(linkPath) //nolint:gosec // G304 — test temp file
	if err != nil {
		t.Fatalf("read hard link: %v", err)
	}
	if string(content) != "world" {
		t.Fatalf("hard link content = %q, want %q", content, "world")
	}

	if _, err := os.Stat(filepath.Join(dest, "dir", "sub", "dir", "file")); !os.IsNotExist(err) {
		t.Fatalf(
			"hard link resolved relative to its own directory instead of the archive root: %v",
			err,
		)
	}
}

func TestExtract_ValidSymlinkAllowed(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{name: targetFileName, body: "content"},
		{
			name:       "link.txt",
			linkTarget: targetFileName,
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
	if target != targetFileName {
		t.Fatalf("symlink target = %q, want %q", target, targetFileName)
	}
}

func TestExtract_PreserveHeaderOwnership(t *testing.T) {
	t.Parallel()
	buf := newTarGz(t, []tarEntry{
		{name: "dir", uid: os.Getuid(), gid: os.Getgid()},
	})

	dest := t.TempDir()
	if err := Extract(buf, dest, PreserveHeaderOwnership()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(filepath.Join(dest, "dir"))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		//nolint:gosec // G115 — uid/gid are uint32 on every supported platform
		if stat.Uid != uint32(os.Getuid()) || stat.Gid != uint32(os.Getgid()) {
			t.Fatalf(
				"dir owner = %d:%d, want %d:%d",
				stat.Uid, stat.Gid, os.Getuid(), os.Getgid(),
			)
		}
	}
}

func TestExtract_PreserveHeaderOwnershipUnprivilegedDegrades(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission errors cannot occur")
	}
	buf := newTarGz(t, []tarEntry{
		{name: "hello.txt", body: "world", uid: 0, gid: 0},
	})

	dest := t.TempDir()
	if err := Extract(buf, dest, PreserveHeaderOwnership()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path := filepath.Join(dest, "hello.txt")
	content, err := os.ReadFile(path) //nolint:gosec // G304 — test temp file
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(content) != "world" {
		t.Fatalf("got %q, want %q", string(content), "world")
	}
}
