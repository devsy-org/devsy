package gitsshsigning

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testCommitPayload    = "tree abc123\nauthor Test <test@example.com>\n\ntest commit"
	testTildeKeyPath     = "~/.ssh/git_signing_ed25519"
	testContainerKeyPath = "/home/vscode/.ssh/git_signing_ed25519"
	testCustomNamespace  = "file"
	testPrivateKeyMarker = "PRIVATE KEY"
	testEd25519Prefix    = "ssh-ed25519 "
	testSignerPrincipal  = "test@example.com"
	envSSHAuthSock       = "SSH_AUTH_SOCK"
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
		Content: testCommitPayload,
		KeyPath: "/tmp/.git_signing_key_does_not_exist",
	}

	_, err := req.Sign()
	require.ErrorContains(t, err, "no public key content or host signing key file")
}

func TestResolveKeyFile_PublicKey_CreatesAndCleansTempFile(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKey test@example.com"
	req := &GitSSHSignatureRequest{
		PublicKey: publicKey,
		KeyPath:   "/nonexistent/original/key",
	}

	resolved, cleanup, err := req.resolveKeyFile("")
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, req.KeyPath, resolved)
	require.FileExists(t, resolved)

	f, err := os.Open(resolved) //nolint:gosec // test-controlled temp file path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, publicKey+"\n", string(got))
}

func TestResolveKeyFile_PublicKey_CleanupRemovesTempFile(t *testing.T) {
	req := &GitSSHSignatureRequest{
		PublicKey: "ssh-ed25519 AAAAC3 test@example.com",
	}

	resolved, cleanup, err := req.resolveKeyFile("")
	require.NoError(t, err)
	require.FileExists(t, resolved)

	cleanup()

	_, statErr := os.Stat(resolved)
	assert.True(t, os.IsNotExist(statErr), "temp file should be removed after cleanup")
}

func TestResolveKeyFile_EmptyPublicKey_RequiresHostKey(t *testing.T) {
	req := &GitSSHSignatureRequest{KeyPath: "/host/untrusted/key"}
	_, cleanup, err := req.resolveKeyFile("")

	require.ErrorContains(t, err, "no public key content or host signing key file")
	cleanup()
}

func TestSign_HostTildePrivateKey_WithoutSSHAgent(t *testing.T) {
	hostHome := t.TempDir()
	setTestHome(t, hostHome)
	t.Setenv(envSSHAuthSock, "")

	hostKeyPath := filepath.Join(hostHome, testSSHDirName, testSigningKeyName)
	pubLine := generateEd25519TestKey(t, hostKeyPath, "")

	req := &GitSSHSignatureRequest{
		Content: testCommitPayload,
		KeyPath: testTildeKeyPath,
	}

	resp, err := req.SignWithHostKey(testTildeKeyPath)
	require.NoError(t, err)
	assertValidSSHSignature(t, pubLine, req, resp.Signature)
}

func TestSignWithHostKey_ContainerPrivateKeyDifferentHome_WithoutSSHAgent(t *testing.T) {
	hostHome := t.TempDir()
	setTestHome(t, hostHome)
	t.Setenv(envSSHAuthSock, "")

	hostKeyPath := filepath.Join(hostHome, testSSHDirName, testSigningKeyName)
	pubLine := generateEd25519TestKey(t, hostKeyPath, "")
	derivedPub := extractPublicKeyForRequest(hostKeyPath)
	require.NotEmpty(t, derivedPub)

	reqWithPub := &GitSSHSignatureRequest{
		Content:   testCommitPayload,
		KeyPath:   testContainerKeyPath,
		PublicKey: derivedPub,
	}
	resp, err := reqWithPub.SignWithHostKey(testTildeKeyPath)
	require.NoError(t, err)
	assertValidSSHSignature(t, pubLine, reqWithPub, resp.Signature)

	reqWithoutPub := &GitSSHSignatureRequest{
		Content: testCommitPayload,
		KeyPath: testContainerKeyPath,
	}
	respFallback, err := reqWithoutPub.SignWithHostKey(testTildeKeyPath)
	require.NoError(t, err)
	assertValidSSHSignature(t, pubLine, reqWithoutPub, respFallback.Signature)
}

func TestSign_SamePathHostAndContainerPrivateKey_WithoutSSHAgent(t *testing.T) {
	t.Setenv(envSSHAuthSock, "")

	sharedKeyPath := filepath.Join(t.TempDir(), testSSHDirName, testSigningKeyName)
	pubLine := generateEd25519TestKey(t, sharedKeyPath, "")
	derivedPub := extractPublicKeyForRequest(sharedKeyPath)
	require.NotEmpty(t, derivedPub)

	req := &GitSSHSignatureRequest{
		Content:   testCommitPayload,
		KeyPath:   sharedKeyPath,
		PublicKey: derivedPub,
	}

	resolved, cleanup, err := req.resolveKeyFile(sharedKeyPath)
	require.NoError(t, err)
	assert.Equal(t, sharedKeyPath, resolved)
	cleanup()
	assert.FileExists(t, sharedKeyPath)

	resp, err := req.SignWithHostKey(sharedKeyPath)
	require.NoError(t, err)
	assertValidSSHSignature(t, pubLine, req, resp.Signature)
}

func TestSign_RejectsInvalidNamespace(t *testing.T) {
	for _, badNS := range []string{"file", "git2", "-U", "-f", "   ", "git extra", "git\nfoo", "git\x7f"} {
		req := &GitSSHSignatureRequest{
			Content:   testCommitPayload,
			Namespace: badNS,
		}
		_, err := req.Sign()
		require.ErrorContains(t, err, "invalid signing namespace")
	}
}

func TestResolveKeyFile_DoesNotUseRequestKeyPath(t *testing.T) {
	hostKeyPath := filepath.Join(t.TempDir(), "host-signing-key")
	requestKeyPath := filepath.Join(t.TempDir(), "container-selected-key")
	_ = generateEd25519TestKey(t, hostKeyPath, "")
	_ = generateEd25519TestKey(t, requestKeyPath, "")

	req := &GitSSHSignatureRequest{
		KeyPath:   requestKeyPath,
		PublicKey: extractPublicKeyForRequest(requestKeyPath),
	}
	resolved, cleanup, err := req.resolveKeyFile(hostKeyPath)
	require.NoError(t, err)
	defer cleanup()

	assert.NotEqual(t, hostKeyPath, resolved)
	assert.NotEqual(t, requestKeyPath, resolved)
	require.FileExists(t, resolved)
}

func generateEd25519TestKey(t *testing.T, keyPath, passphrase string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(keyPath), 0o700))
	//nolint:gosec // test helper generating ephemeral ed25519 key
	out, err := exec.Command(
		"ssh-keygen",
		"-q",
		"-t",
		"ed25519",
		"-N",
		passphrase,
		"-C",
		testSignerPrincipal,
		"-f",
		keyPath,
	).CombinedOutput()
	require.NoError(t, err, "ssh-keygen keygen failed: %s", string(out))
	// #nosec G304 -- test path under t.TempDir()
	pubBytes, err := os.ReadFile(keyPath + ".pub")
	require.NoError(t, err)
	return strings.TrimSpace(string(pubBytes))
}

func assertValidSSHSignature(
	t *testing.T,
	pubKeyLine string,
	req *GitSSHSignatureRequest,
	sig []byte,
) {
	t.Helper()
	require.NoError(t, verifySSHSignatureRaw(t, pubKeyLine, req, sig))
}

func verifySSHSignatureRaw(
	t *testing.T,
	pubKeyLine string,
	req *GitSSHSignatureRequest,
	sig []byte,
) error {
	t.Helper()
	namespace := req.Namespace
	if namespace == "" {
		namespace = defaultSigningNamespace
	}

	verifyDir := t.TempDir()
	allowedSigners := filepath.Join(verifyDir, "allowed_signers")
	sigPath := filepath.Join(verifyDir, "message.sig")

	entry := testSignerPrincipal + " " + strings.TrimSpace(pubKeyLine) + "\n"
	require.NoError(t, os.WriteFile(allowedSigners, []byte(entry), 0o600))
	require.NoError(t, os.WriteFile(sigPath, sig, 0o600))

	//nolint:gosec // test helper verifying signature with ssh-keygen
	cmd := exec.Command(
		"ssh-keygen",
		"-Y",
		"verify",
		"-f",
		allowedSigners,
		"-I",
		testSignerPrincipal,
		"-n",
		namespace,
		"-s",
		sigPath,
	)
	cmd.Stdin = strings.NewReader(req.Content)
	_, err := cmd.CombinedOutput()
	return err
}
