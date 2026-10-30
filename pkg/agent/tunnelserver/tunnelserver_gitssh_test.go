package tunnelserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/gitsshsigning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitSSHSignature_DeserializesPublicKey(t *testing.T) {
	req := gitsshsigning.GitSSHSignatureRequest{
		Content:   "tree abc\nauthor Test\n\ncommit",
		KeyPath:   "/container/tmp/.git_signing_key_tmpXXX",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake test@test.com",
	}
	reqJSON, err := json.Marshal(req)
	require.NoError(t, err)

	ts := New()

	_, err = ts.GitSSHSignature(context.Background(), &tunnel.Message{
		Message: string(reqJSON),
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "git ssh sign request")
	assert.NotContains(t, err.Error(), "resolve signing key")
	assert.Contains(t, err.Error(), "failed to sign commit")
}

func TestGitSSHSignature_SignsWithHostKeyWithoutSSHAgent(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("SSH_AUTH_SOCK", "")

	sshDir := filepath.Join(hostHome, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))

	privKeyPath := filepath.Join(sshDir, "git_signing_ed25519")
	//nolint:gosec // test-only ssh-keygen invocation under t.TempDir.
	genOut, err := exec.Command(
		"ssh-keygen",
		"-t",
		"ed25519",
		"-N",
		"",
		"-f",
		privKeyPath,
	).CombinedOutput()
	require.NoError(t, err, "ssh-keygen failed: %s", string(genOut))

	// #nosec G304 -- test file path under t.TempDir.
	pubKeyBytes, err := os.ReadFile(privKeyPath + ".pub")
	require.NoError(t, err)

	req := gitsshsigning.GitSSHSignatureRequest{
		Content:   "tree abc\nauthor Test <test@example.com> 1700000000 +0000\n\ncommit\n",
		KeyPath:   "/home/vscode/.ssh/git_signing_ed25519",
		PublicKey: strings.TrimSpace(string(pubKeyBytes)),
		Namespace: "git",
	}
	reqJSON, err := json.Marshal(req)
	require.NoError(t, err)

	ts := New(WithGitSSHSigningKey("~/.ssh/git_signing_ed25519"))

	respMsg, err := ts.GitSSHSignature(context.Background(), &tunnel.Message{
		Message: string(reqJSON),
	})
	require.NoError(t, err)

	var sigResp gitsshsigning.GitSSHSignatureResponse
	require.NoError(t, json.Unmarshal([]byte(respMsg.Message), &sigResp))
	assert.Contains(t, string(sigResp.Signature), "-----BEGIN SSH SIGNATURE-----")
	assert.Contains(t, string(sigResp.Signature), "-----END SSH SIGNATURE-----")
}
