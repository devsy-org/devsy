package gitsshsigning

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSign_WithPublicKeyContent_WritesToTempFile(t *testing.T) {
	req := &GitSSHSignatureRequest{
		Content:   "tree abc123\nauthor Test <test@example.com>\n\ntest commit",
		KeyPath:   "/tmp/.git_signing_key_does_not_exist",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTesting test@example.com",
	}

	_, err := req.Sign()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "/tmp/.git_signing_key_does_not_exist")
}

func TestSign_NonExistentKeyPath_ReturnsError(t *testing.T) {
	req := &GitSSHSignatureRequest{
		Content: "tree abc123\nauthor Test <test@example.com>\n\ntest commit",
		KeyPath: "/tmp/.git_signing_key_does_not_exist",
	}

	_, err := req.Sign()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sign commit")
}

func TestResolveKeyFile_PublicKey_CreatesAndCleansTempFile(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKey test@example.com"
	req := &GitSSHSignatureRequest{
		PublicKey: publicKey,
		KeyPath:   "/nonexistent/original/key",
	}

	resolved, cleanup, err := req.resolveKeyFile()
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, req.KeyPath, resolved)
	require.FileExists(t, resolved)

	f, err := os.Open(resolved) //nolint:gosec // test-controlled temp file path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, publicKey, string(got))
}

func TestResolveKeyFile_PublicKey_CleanupRemovesTempFile(t *testing.T) {
	req := &GitSSHSignatureRequest{
		PublicKey: "ssh-ed25519 AAAAC3 test@example.com",
	}

	resolved, cleanup, err := req.resolveKeyFile()
	require.NoError(t, err)
	require.FileExists(t, resolved)

	cleanup()

	_, statErr := os.Stat(resolved)
	assert.True(t, os.IsNotExist(statErr), "temp file should be removed after cleanup")
}

func TestResolveKeyFile_EmptyPublicKey_ReturnsKeyPath(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "signing-key")
	require.NoError(t, os.WriteFile(keyPath, []byte("key"), 0o600))

	req := &GitSSHSignatureRequest{KeyPath: keyPath}
	resolved, cleanup, err := req.resolveKeyFile()

	require.NoError(t, err)
	assert.Equal(t, keyPath, resolved)
	cleanup()
	assert.FileExists(t, keyPath, "original key file must not be removed by cleanup")
}
