package ghcredentials

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeConfigDirectoryPrecedenceAndEmptyValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GH_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	got, err := ConfigDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".config", "gh"), got)
	app := filepath.Join(home, "app data")
	t.Setenv("APPDATA", app)
	got, err = ConfigDir()
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		require.Equal(t, filepath.Join(app, "GitHub CLI"), got)
	}
	xdg := filepath.Join(home, "xdg space")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err = ConfigDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(xdg, "gh"), got)
	t.Setenv("GH_CONFIG_DIR", "relative space")
	got, err = ConfigDir()
	require.NoError(t, err)
	want, err := filepath.Abs("relative space")
	require.NoError(t, err)
	require.Equal(t, want, got)
	t.Setenv("GH_CONFIG_DIR", filepath.Join(home, "override space"))
	got, err = ConfigDir()
	require.NoError(t, err)
	require.Equal(t, os.Getenv("GH_CONFIG_DIR"), got)
}

func TestNativeConfigRejectsTrailingDocumentsAndMalformedContent(t *testing.T) {
	for _, data := range []string{
		"{}\ninvalid.test:\n  users: [alice, bob]\n",
		"{}\n---\neditor: newer\n",
		"{}\n---\n",
		"{}\n...\n[malformed",
	} {
		_, err := decode([]byte(data))
		require.ErrorIs(t, err, errInvalidConfig)
	}
}

func TestNativeConfigAcceptsCompleteSingleDocuments(t *testing.T) {
	for _, data := range []string{"", "# comment\n", "{}\n", "---\neditor: vi\n...\n# tail\n", "custom: [one, two]\n"} {
		_, err := decode([]byte(data))
		require.NoError(t, err)
	}
}
