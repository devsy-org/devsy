package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserHasLinger(t *testing.T) {
	dir := t.TempDir()
	orig := lingerDir
	lingerDir = dir
	t.Cleanup(func() { lingerDir = orig })

	if userHasLinger("alice") {
		t.Fatal("expected no linger when marker file is absent")
	}

	if err := os.WriteFile(filepath.Join(dir, "alice"), nil, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if !userHasLinger("alice") {
		t.Fatal("expected linger when marker file exists")
	}
	if userHasLinger("bob") {
		t.Fatal("expected no linger for a different user")
	}
}
