package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "hack", "licenses")
	fatalOnErr(t, os.MkdirAll(nested, 0o755))
	fatalOnErr(t, os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/x\n"), 0o644))

	tests := []struct {
		name    string
		start   string
		want    string
		wantErr bool
	}{
		{name: "start at root", start: root, want: root},
		{name: "start in nested directory", start: nested, want: root},
		{name: "no go.mod above start", start: t.TempDir(), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repoRoot(tt.start)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("repoRoot(%q) = %q, want %q", tt.start, got, tt.want)
			}
		})
	}
}
