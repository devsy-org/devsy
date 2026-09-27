package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverDevContainerPath(t *testing.T) {
	tests := []struct {
		name        string
		files       []string
		want        string
		wantErrText string
	}{
		{
			name:  "devcontainer directory root takes precedence",
			files: []string{".devcontainer/devcontainer.json", ".devcontainer.json", ".devcontainer/profile/devcontainer.json"},
			want:  ".devcontainer/devcontainer.json",
		},
		{
			name:  "root file takes precedence over nested config",
			files: []string{".devcontainer.json", ".devcontainer/profile/devcontainer.json"},
			want:  ".devcontainer.json",
		},
		{
			name:  "single nested config",
			files: []string{".devcontainer/profile/devcontainer.json"},
			want:  ".devcontainer/profile/devcontainer.json",
		},
		{
			name:        "ambiguous nested configs",
			files:       []string{".devcontainer/one/devcontainer.json", ".devcontainer/two/devcontainer.json"},
			wantErrText: "multiple devcontainer configurations found",
		},
		{
			name: "no config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folder := t.TempDir()
			for _, relativePath := range tt.files {
				file := filepath.Join(folder, filepath.FromSlash(relativePath))
				if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(`{"image":"test"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := DiscoverDevContainerPath(folder)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("DiscoverDevContainerPath() error = %v, want containing %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("DiscoverDevContainerPath(): %v", err)
			}
			if tt.want == "" {
				if got != "" {
					t.Fatalf("DiscoverDevContainerPath() = %q, want empty", got)
				}
				return
			}
			want := filepath.Join(folder, filepath.FromSlash(tt.want))
			if got != want {
				t.Fatalf("DiscoverDevContainerPath() = %q, want %q", got, want)
			}
		})
	}
}
