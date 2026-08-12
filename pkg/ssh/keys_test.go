package ssh

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestGetPrivateKeyRawBase_GeneratesValidKeyPair(t *testing.T) {
	dir := t.TempDir()

	raw, err := GetPrivateKeyRawBase(dir)
	require.NoError(t, err)

	block, _ := pem.Decode(raw)
	require.NotNil(t, block, "private key must be valid PEM")
	assert.Equal(t, "RSA PRIVATE KEY", block.Type)

	info, err := os.Stat(filepath.Join(dir, DevsySSHPrivateKeyFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"private key file must be 0600")

	pubInfo, err := os.Stat(filepath.Join(dir, DevsySSHPublicKeyFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), pubInfo.Mode().Perm(),
		"public key file must be 0644")
}

func TestGetPrivateKeyRawBase_IdempotentAcrossCalls(t *testing.T) {
	dir := t.TempDir()

	first, err := GetPrivateKeyRawBase(dir)
	require.NoError(t, err)

	second, err := GetPrivateKeyRawBase(dir)
	require.NoError(t, err)

	assert.Equal(t, first, second, "existing key pair must not be regenerated")
}

func TestGetPublicKeyBase_ReturnsParseableAuthorizedKey(t *testing.T) {
	dir := t.TempDir()

	encoded, err := GetPublicKeyBase(dir)
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(decoded)
	require.NoError(t, err)
	assert.Equal(t, ssh.KeyAlgoRSA, pubKey.Type())
}

func TestGetPublicKeyBase_ConsistentWithPrivateKey(t *testing.T) {
	dir := t.TempDir()

	privRaw, err := GetPrivateKeyRawBase(dir)
	require.NoError(t, err)

	privAny, err := ssh.ParseRawPrivateKey(privRaw)
	require.NoError(t, err)

	encoded, err := GetPublicKeyBase(dir)
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(decoded)
	require.NoError(t, err)

	priv, ok := privAny.(*rsa.PrivateKey)
	require.True(t, ok, "parsed private key must be RSA")

	expected, err := ssh.NewPublicKey(priv.Public())
	require.NoError(t, err)

	assert.Equal(t, expected.Marshal(), pubKey.Marshal(),
		"public key must correspond to the generated private key")
}

func TestGetHostKeyBase_GeneratesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := GetHostKeyBase(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, first, "host key must not be empty")

	info, err := os.Stat(filepath.Join(dir, DevsySSHHostKeyFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"host key file must be 0600")

	second, err := GetHostKeyBase(dir)
	require.NoError(t, err)
	assert.Equal(t, first, second, "existing host key must not be regenerated")
}

func TestGetPrivateKeyRawBase_CreatesMissingDirectory(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "does", "not", "exist")

	_, err := GetPrivateKeyRawBase(nested)
	require.NoError(t, err)

	_, err = os.Stat(nested)
	assert.NoError(t, err, "base directory must be created when missing")
}
