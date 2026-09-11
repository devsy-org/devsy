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

func TestParseProjectConfig_DevContainerCustomizations(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`{
  // DevContainer with devsy customizations
  "name": "my-project",
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu",
  "customizations": {
    "devsy": {
      "secretSources": [
        {
          "name": "project",
          "type": "sops",
          "path": "./secrets.enc.yaml",
        },
      ],
      "secrets": [
        "sops:project/API_KEY",
      ],
    },
  },
}`))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.SecretSources, 1)
	require.Equal(t, "project", cfg.SecretSources[0].Name)
	require.Equal(t, []string{"sops:project/API_KEY"}, cfg.Secrets)
}

func TestParseProjectConfig_DevContainerWithoutCustomizations(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`{
  "name": "my-project",
  "image": "ubuntu",
  "customizations": {
    "vscode": {
      "extensions": ["golang.go"]
    }
  }
}`))
	require.NoError(t, err)
	require.Nil(t, cfg)
}

func TestProjectConfigRejectsUndefinedSource(t *testing.T) {
	_, err := ParseProjectConfig([]byte(`secrets: [sops:missing/API_TOKEN]`))
	require.ErrorContains(t, err, "undefined source")
}

func TestCleanProjectSourcePath(t *testing.T) {
	clean, err := CleanProjectSourcePath("./config/secrets.enc.yaml")
	require.NoError(t, err)
	require.Equal(t, "config/secrets.enc.yaml", clean)

	for _, bad := range []string{
		"",
		"/config/secrets.enc.yaml",
		"/secrets.enc.yaml",
		"/etc/passwd",
		`C:\secrets.enc.yaml`,
		`\\server\share\secrets.enc.yaml`,
		"../secret",
		"a/../../secret",
		"..",
		".",
	} {
		_, err := CleanProjectSourcePath(bad)
		require.Error(t, err, bad)
	}
	for _, good := range []string{
		"./secrets.enc.yaml",
		"config/secrets.enc.yaml",
		"secrets.enc.yaml",
	} {
		clean, err := CleanProjectSourcePath(good)
		require.NoError(t, err, good)
		require.False(t, filepath.IsAbs(clean))
	}
}

func TestLoadProjectConfigFromRoot_DevContainerCustomizations(t *testing.T) {
	root := t.TempDir()
	devcontainerDir := filepath.Join(root, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(devcontainerDir, "devcontainer.json"),
		[]byte(`{
  "customizations": {
    "devsy": {
      "secretSources": [
        {"name": "project", "type": "sops", "path": "secrets.enc.yaml"}
      ],
      "secrets": ["sops:project/KEY"]
    }
  }
}`),
		0o600,
	))

	cfg, found, err := LoadProjectConfigFromRoot(root)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, cfg)
	require.Len(t, cfg.SecretSources, 1)
	require.Equal(t, "project", cfg.SecretSources[0].Name)
	require.Equal(t, []string{"sops:project/KEY"}, cfg.Secrets)
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
