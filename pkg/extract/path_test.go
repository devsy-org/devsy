package extract

import (
	"archive/tar"
	"strings"
	"testing"
)

const (
	targetFileName = "target.txt"
	etcPasswd      = "../../etc/passwd" // #nosec G101
)

func TestWithinDir(t *testing.T) {
	t.Parallel()
	const dest = "/tmp/dest"
	cases := []struct {
		name     string
		resolved string
		want     bool
	}{
		{"nested inside", dest + "/sub/file", true},
		{"exactly dest", dest, true},
		{"deep inside", dest + "/a/b/c", true},
		{"sibling prefix outside", dest + "_evil/file", false},
		{"unrelated outside", "/tmp/other/file", false},
		{"parent dir", "/tmp", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := withinDir(tt.resolved, dest); got != tt.want {
				t.Errorf("withinDir(%q, %q) = %v, want %v", tt.resolved, dest, got, tt.want)
			}
		})
	}
}

func TestResolveLinkTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		linkname string
		outFile  string
		want     string
	}{
		{"absolute target preserved", "/etc/passwd", "/tmp/dest/link", "/etc/passwd"},
		{"relative parent", "../target", "/tmp/dest/sub/link", "/tmp/dest/target"},
		{"relative sibling", targetFileName, "/tmp/dest/link", "/tmp/dest/target.txt"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveLinkTarget(tt.linkname, tt.outFile); got != tt.want {
				t.Errorf(
					"resolveLinkTarget(%q, %q) = %q, want %q",
					tt.linkname,
					tt.outFile,
					got,
					tt.want,
				)
			}
		})
	}
}

func TestResolveRelativePath_StripLevels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header *tar.Header
		strip  int
		want   string
	}{
		{"no strip keeps path", &tar.Header{Name: "dir/file"}, 0, "/dir/file"},
		{"strip one level", &tar.Header{Name: "a/b/c"}, 1, "/b/c"},
		{"strip two levels", &tar.Header{Name: "a/b/c"}, 2, "/c"},
		{"strip beyond depth keeps remainder", &tar.Header{Name: "a"}, 2, "/a"},
		{"strip to final segment", &tar.Header{Name: "a/b"}, 1, "/b"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := &Options{StripLevels: tt.strip}
			if got := resolveRelativePath(tt.header, opts); got != tt.want {
				t.Errorf(
					"resolveRelativePath(%q, strip=%d) = %q, want %q",
					tt.header.Name,
					tt.strip,
					got,
					tt.want,
				)
			}
		})
	}
}

func TestValidateLinkTarget(t *testing.T) {
	t.Parallel()
	const dest = "/tmp/dest"
	cases := []struct {
		name      string
		typeflag  byte
		linkname  string
		wantError string
	}{
		{"symlink inside allowed", tar.TypeSymlink, targetFileName, ""},
		{"symlink outside blocked", tar.TypeSymlink, etcPasswd, "symlink traversal"},
		{"hard link outside blocked", tar.TypeLink, etcPasswd, "hard link traversal"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			header := &tar.Header{Typeflag: tt.typeflag, Name: "link", Linkname: tt.linkname}
			outFile := dest + "/link"
			err := validateLinkTarget(header, outFile, dest)
			if tt.wantError == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error %q does not mention %q", err, tt.wantError)
			}
		})
	}
}
