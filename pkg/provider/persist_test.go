package provider

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

type persistSample struct {
	Name string `json:"name"`
}

func TestRequireID(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		id      string
		wantErr string
	}{
		{name: "non-empty id passes", kind: "machine id", id: "m1"},
		{name: "empty machine id", kind: "machine id", wantErr: "machine id is empty"},
		{name: "empty workspace id", kind: "workspace id", wantErr: "workspace id is empty"},
		{
			name:    "empty pro instance host",
			kind:    "pro instance host",
			wantErr: "pro instance host is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireID(tt.kind, tt.id)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestSaveJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		perm os.FileMode
	}{
		{name: "owner-only file", perm: 0o600},
		{name: "world-readable file", perm: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "missing", "parent")
			path := filepath.Join(dir, "sample.json")
			want := &persistSample{Name: "value"}

			require.NoError(t, saveJSON(dir, "sample.json", want, tt.perm))

			got, err := loadJSON[persistSample](path)
			require.NoError(t, err)
			require.Equal(t, want, got)

			if runtime.GOOS != windowsOS {
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, tt.perm, info.Mode().Perm())
			}
		})
	}
}

func TestWriteJSONFileRequiresExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")

	err := writeJSONFile(filepath.Join(dir, "sample.json"), &persistSample{Name: "x"}, 0o600)
	require.Error(t, err)
	require.NoDirExists(t, dir)
}

func TestLoadJSONErrors(t *testing.T) {
	tests := []struct {
		name         string
		content      *string
		wantNotExist bool
	}{
		{name: "missing file", content: nil, wantNotExist: true},
		{name: "invalid JSON", content: new("{not json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sample.json")
			if tt.content != nil {
				require.NoError(t, os.WriteFile(path, []byte(*tt.content), 0o600))
			}

			got, err := loadJSON[persistSample](path)
			require.Error(t, err)
			require.Nil(t, got)
			require.Equal(t, tt.wantNotExist, errors.Is(err, fs.ErrNotExist))
		})
	}
}
