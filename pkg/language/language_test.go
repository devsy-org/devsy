package language

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func writeFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDetectLanguageEmptyDirReturnsNone(t *testing.T) {
	lang, err := DetectLanguage(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != None {
		t.Fatalf("got %q, want %q", lang, None)
	}
}

func TestDetectLanguageMostFrequentWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go")
	writeFile(t, dir, "b.go")
	writeFile(t, dir, "c.py")

	lang, err := DetectLanguage(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != Go {
		t.Fatalf("got %q, want %q", lang, Go)
	}
}

func TestDetectLanguageTiePicksLexicographicallySmaller(t *testing.T) {
	// Equal counts of Go and Java files: "Go" < "Java", so Go wins regardless
	// of map iteration order.
	dir := t.TempDir()
	writeFile(t, dir, "a.go")
	writeFile(t, dir, "b.java")

	lang, err := DetectLanguage(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != Go {
		t.Fatalf("got %q, want %q (tie must favor lexicographically smaller)", lang, Go)
	}
}

func TestDetectLanguageMapsTypeScriptToJavaScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.ts")

	lang, err := DetectLanguage(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != JavaScript {
		t.Fatalf("got %q, want %q (TypeScript must map to JavaScript)", lang, JavaScript)
	}
}

func TestDetectLanguageMapsCToCpp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.c")

	lang, err := DetectLanguage(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != Cpp {
		t.Fatalf("got %q, want %q (C must map to C++)", lang, Cpp)
	}
}

func TestDetectLanguageSkipsIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keep.py")

	nodeModules := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(nodeModules, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, nodeModules, "noise.js")
	writeFile(t, nodeModules, "noise2.js")

	lang, err := DetectLanguage(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != Python {
		t.Fatalf("got %q, want %q (node_modules must be skipped)", lang, Python)
	}
}

func TestDetectLanguageErrorsOnRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir.go")
	writeFile(t, dir, "notadir.go")

	if _, err := DetectLanguage(file); err == nil {
		t.Fatal("expected error for regular file path, got nil")
	}
}

func TestDetectLanguageErrorsOnNonexistentPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := DetectLanguage(missing); err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

func TestDefaultConfigGoProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go")

	got := DefaultConfig(dir)
	want := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "mcr.microsoft.com/devcontainers/go",
		},
	}
	if got == nil {
		t.Fatal("got nil config")
	}
	if got.Image != want.Image {
		t.Fatalf("got image %q, want %q", got.Image, want.Image)
	}
}

func TestDefaultConfigEmptyDirFallsBackToNone(t *testing.T) {
	got := DefaultConfig(t.TempDir())
	if got == nil {
		t.Fatal("got nil config")
	}
	if got.Image != MapConfig[None].Image {
		t.Fatalf("got image %q, want fallback %q", got.Image, MapConfig[None].Image)
	}
}

func TestShouldSkipDir(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{".", false},
		{"src", false},
		{"node_modules", true},
		{".git", true},
		{".hidden", true},
	}
	for _, c := range cases {
		if got := shouldSkipDir(c.name); got != c.want {
			t.Errorf("shouldSkipDir(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
