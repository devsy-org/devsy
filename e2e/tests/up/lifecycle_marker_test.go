package up

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycleMarkerCount(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir, marker string)
		wantCount int
		wantErr   bool
	}{
		{
			name:      "missing file returns 0",
			setup:     func(t *testing.T, dir, marker string) {},
			wantCount: 0,
		},
		{
			name: "one line returns 1",
			setup: func(t *testing.T, dir, marker string) {
				requireWriteFile(t, filepath.Join(dir, marker), "attach\n")
			},
			wantCount: 1,
		},
		{
			name: "two lines returns 2",
			setup: func(t *testing.T, dir, marker string) {
				requireWriteFile(t, filepath.Join(dir, marker), "attach\nattach\n")
			},
			wantCount: 2,
		},
		{
			name: "trailing newline and blank lines handled correctly",
			setup: func(t *testing.T, dir, marker string) {
				requireWriteFile(t, filepath.Join(dir, marker), "\nattach\n  \nattach\n\n")
			},
			wantCount: 2,
		},
		{
			name: "filesystem error propagated",
			setup: func(t *testing.T, dir, marker string) {
				if err := os.Mkdir(filepath.Join(dir, marker), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			marker := ".devsy-post-attach.log"
			tt.setup(t, tempDir, marker)

			count, err := lifecycleMarkerCount(tempDir, marker)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if count != tt.wantCount {
				t.Fatalf("expected %d, got %d", tt.wantCount, count)
			}
		})
	}
}

func requireWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
