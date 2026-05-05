package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func writeGitConfig(t *testing.T, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_DIR", filepath.Join(home, ".git"))
	err := os.MkdirAll(filepath.Join(home, ".git"), 0o750)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(content), 0o600)
	assert.NoError(t, err)
}

func TestGpgSigningKey_GPGFormat(t *testing.T) {
	writeGitConfig(t, "[user]\n\tsigningKey = TESTKEY123\n")
	result := gpgSigningKey()
	assert.Equal(t, "TESTKEY123", result)
}

func TestGpgSigningKey_SSHFormat_Skipped(t *testing.T) {
	writeGitConfig(
		t,
		"[gpg]\n\tformat = ssh\n[user]\n\tsigningKey = /home/user/.ssh/id_ed25519.pub\n",
	)
	result := gpgSigningKey()
	assert.Empty(t, result)
}

func TestGpgSigningKey_NoKeyConfigured(t *testing.T) {
	writeGitConfig(t, "[user]\n\tname = Test\n")
	result := gpgSigningKey()
	assert.Empty(t, result)
}

func TestGpgSigningKey_X509Format_Returned(t *testing.T) {
	writeGitConfig(t, "[gpg]\n\tformat = x509\n[user]\n\tsigningKey = /path/to/cert\n")
	result := gpgSigningKey()
	assert.Equal(t, "/path/to/cert", result)
}

func TestGpgSigningKey_SSHKeyPath_Skipped(t *testing.T) {
	writeGitConfig(t, "[user]\n\tsigningKey = /home/user/.ssh/id_ed25519.pub\n")
	result := gpgSigningKey()
	assert.Empty(t, result)
}

func TestGpgSigningKey_TildeKeyPath_Skipped(t *testing.T) {
	writeGitConfig(t, "[user]\n\tsigningKey = ~/.ssh/id_ed25519.pub\n")
	result := gpgSigningKey()
	assert.Empty(t, result)
}
