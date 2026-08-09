package hash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"hello", "hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.in); got != tc.want {
				t.Fatalf("String(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStringIsDeterministic(t *testing.T) {
	first := String("devsy")
	second := String("devsy")
	if first != second {
		t.Fatalf("String not deterministic: %q != %q", first, second)
	}
}

func TestStringToNumber(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want uint32
	}{
		{"empty", "", 2166136261},
		{"hello", "hello", 1335831723},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StringToNumber(tc.in); got != tc.want {
				t.Fatalf("StringToNumber(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	content := []byte("hello")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	got, err := File(path)
	if err != nil {
		t.Fatalf("File(%q) error: %v", path, err)
	}
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("File(%q) = %q, want %q", path, got, want)
	}
}

func TestFileMissingPath(t *testing.T) {
	if _, err := File(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("File: expected error for missing path, got nil")
	}
}
