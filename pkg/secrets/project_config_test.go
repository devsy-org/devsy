package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`
secretSources:
  - name: project
    type: sops
    path: ./secrets.enc.yaml
secrets:
  - sops:project/API_TOKEN
`))
	require.NoError(t, err)
	require.Len(t, cfg.SecretSources, 1)
	require.Equal(t, "project", cfg.SecretSources[0].Name)
	require.Equal(t, []string{"sops:project/API_TOKEN"}, cfg.Secrets)
}

func TestProjectConfigRejectsUndefinedSource(t *testing.T) {
	_, err := ParseProjectConfig([]byte(`secrets: [sops:missing/API_TOKEN]`))
	require.ErrorContains(t, err, "undefined source")
}

func TestCleanProjectSourcePath(t *testing.T) {
	clean, err := CleanProjectSourcePath("./config/secrets.enc.yaml")
	require.NoError(t, err)
	require.Equal(t, "config/secrets.enc.yaml", clean)

	cleanAbs, err := CleanProjectSourcePath("/config/secrets.enc.yaml")
	require.NoError(t, err)
	require.Equal(t, "config/secrets.enc.yaml", cleanAbs)

	for _, bad := range []string{"", "../secret", "a/../../secret"} {
		_, err := CleanProjectSourcePath(bad)
		require.Error(t, err, bad)
	}
}

func TestResolveProjectSourcePathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	outsideSecret := filepath.Join(outside, "secret.yaml")
	require.NoError(t, os.WriteFile(outsideSecret, []byte("secret"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o700))
	require.NoError(t, os.Symlink(outsideSecret, filepath.Join(root, "config", "secret.yaml")))

	_, err := ResolveProjectSourcePath(root, "config/secret.yaml")
	require.ErrorContains(t, err, "escapes the repository root")
}
