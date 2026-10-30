package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestIntegration_SigningFailure_SurfacesServerError(t *testing.T) {
	mock := &mockCredentialsClient{
		gitSSHSignatureFunc: func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error) {
			return nil, fmt.Errorf(
				"failed to sign commit: exit status 1, stderr: Permission denied (publickey)",
			)
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := handleGitSSHSignatureRequest(context.Background(), w, r, mock)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	gitsshsigning.SetSignatureServerURL(server.URL + "/git-ssh-signature")
	t.Cleanup(func() { gitsshsigning.SetSignatureServerURL("") })

	tmpDir := t.TempDir()
	bufferFile := filepath.Join(tmpDir, "buffer")
	require.NoError(t, os.WriteFile(bufferFile, []byte("commit content"), 0o600))

	err := gitsshsigning.HandleGitSSHProgramCall("/tmp/key.pub", "git", bufferFile)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
	assert.NotContains(t, err.Error(), "invalid character")

	_, statErr := os.Stat(bufferFile + ".sig")
	assert.True(t, os.IsNotExist(statErr), "expected no .sig file to be created")
}

func TestIntegration_SigningSuccess_WritesSigFile(t *testing.T) {
	expectedSig := []byte(
		"-----BEGIN SSH SIGNATURE-----\ntest-signature\n-----END SSH SIGNATURE-----\n",
	)

	mock := &mockCredentialsClient{
		gitSSHSignatureFunc: func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error) {
			response := gitsshsigning.GitSSHSignatureResponse{Signature: expectedSig}
			jsonBytes, err := json.Marshal(response)
			if err != nil {
				return nil, err
			}
			return &tunnel.Message{Message: string(jsonBytes)}, nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := handleGitSSHSignatureRequest(context.Background(), w, r, mock)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	gitsshsigning.SetSignatureServerURL(server.URL + "/git-ssh-signature")
	t.Cleanup(func() { gitsshsigning.SetSignatureServerURL("") })

	tmpDir := t.TempDir()
	bufferFile := filepath.Join(tmpDir, "buffer")
	require.NoError(t, os.WriteFile(bufferFile, []byte("commit content"), 0o600))

	err := gitsshsigning.HandleGitSSHProgramCall("/tmp/key.pub", "git", bufferFile)

	require.NoError(t, err)

	sigContent, readErr := os.ReadFile(
		bufferFile + ".sig",
	) // #nosec G304 -- test file path from t.TempDir
	require.NoError(t, readErr)
	assert.Equal(t, expectedSig, sigContent)
}

func TestIntegration_SignatureRequest_IncludesPublicKeyContent(t *testing.T) {
	var receivedMessage string
	mock := &mockCredentialsClient{
		gitSSHSignatureFunc: func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error) {
			receivedMessage = msg.Message
			sig := gitsshsigning.GitSSHSignatureResponse{
				Signature: []byte(
					"-----BEGIN SSH SIGNATURE-----\ntest\n-----END SSH SIGNATURE-----\n",
				),
			}
			jsonBytes, err := json.Marshal(sig)
			if err != nil {
				return nil, fmt.Errorf("marshal sig: %w", err)
			}
			return &tunnel.Message{Message: string(jsonBytes)}, nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := handleGitSSHSignatureRequest(context.Background(), w, r, mock)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	gitsshsigning.SetSignatureServerURL(server.URL + "/git-ssh-signature")
	t.Cleanup(func() { gitsshsigning.SetSignatureServerURL("") })

	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "test_key.pub")
	pubKeyContent := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@example.com"
	require.NoError(t, os.WriteFile(certFile, []byte(pubKeyContent), 0o600))

	bufferFile := filepath.Join(tmpDir, "buffer")
	require.NoError(t, os.WriteFile(bufferFile, []byte("commit content"), 0o600))

	err := gitsshsigning.HandleGitSSHProgramCall(certFile, "git", bufferFile)
	require.NoError(t, err)

	var req gitsshsigning.GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal([]byte(receivedMessage), &req))
	assert.Equal(t, pubKeyContent, req.PublicKey)
	assert.Equal(t, "commit content", req.Content)
}

func TestIntegration_EndToEndPrivateKeyRelayWithoutSSHAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	hostPrivKey, pubKeyLine := generateDisposableEd25519KeyPair(t, t.TempDir())

	// Simulate shared dotfiles inside the container where certFile points to an
	// OpenSSH private key at a different $HOME path without a sibling .pub file.
	containerSSHDir := filepath.Join(t.TempDir(), ".ssh")
	require.NoError(t, os.MkdirAll(containerSSHDir, 0o700))
	// #nosec G304 -- test file path under t.TempDir.
	privKeyBytes, err := os.ReadFile(hostPrivKey)
	require.NoError(t, err)
	containerPrivKey := filepath.Join(containerSSHDir, "git_signing_ed25519")
	require.NoError(
		t,
		os.WriteFile(containerPrivKey, privKeyBytes, 0o600),
	) // #nosec G304,G703 -- test file path under t.TempDir.

	var capturedTunnelPayload string
	client := &mockCredentialsClient{
		gitSSHSignatureFunc: func(
			_ context.Context,
			msg *tunnel.Message,
		) (*tunnel.Message, error) {
			capturedTunnelPayload = msg.Message
			var req gitsshsigning.GitSSHSignatureRequest
			if err := json.Unmarshal([]byte(msg.Message), &req); err != nil {
				return nil, err
			}
			resp, err := req.SignWithHostKey(hostPrivKey)
			if err != nil {
				return nil, err
			}
			out, err := json.Marshal(resp)
			if err != nil {
				return nil, err
			}
			return &tunnel.Message{Message: string(out)}, nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := handleGitSSHSignatureRequest(context.Background(), w, r, client); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	gitsshsigning.SetSignatureServerURL(server.URL + "/git-ssh-signature")
	t.Cleanup(func() { gitsshsigning.SetSignatureServerURL("") })

	payload := []byte(
		"tree deadbeef\nauthor Dev <dev@example.com> 1700000000 +0000\n\nsigned commit\n",
	)
	bufferFile := filepath.Join(t.TempDir(), "buffer")
	require.NoError(t, os.WriteFile(bufferFile, payload, 0o600))

	require.NoError(t, gitsshsigning.HandleGitSSHProgramCall(containerPrivKey, "git", bufferFile))
	assert.NotContains(t, capturedTunnelPayload, "PRIVATE KEY")

	verifyGitSSHSignature(t, pubKeyLine, bufferFile)
}

func generateDisposableEd25519KeyPair(t *testing.T, rootDir string) (string, string) {
	t.Helper()

	sshDir := filepath.Join(rootDir, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))

	privKeyPath := filepath.Join(sshDir, "git_signing_ed25519")
	//nolint:gosec // test-only ssh-keygen invocation under t.TempDir.
	out, err := exec.Command(
		"ssh-keygen",
		"-t",
		"ed25519",
		"-N",
		"",
		"-C",
		"dev@example.com",
		"-f",
		privKeyPath,
	).CombinedOutput()
	require.NoError(t, err, "ssh-keygen keygen failed: %s", string(out))

	// #nosec G304 -- test file path under t.TempDir.
	pubBytes, err := os.ReadFile(privKeyPath + ".pub")
	require.NoError(t, err)
	return privKeyPath, strings.TrimSpace(string(pubBytes))
}

func verifyGitSSHSignature(t *testing.T, pubKeyLine, bufferFile string) {
	t.Helper()

	fields := strings.Fields(pubKeyLine)
	require.GreaterOrEqual(t, len(fields), 2)

	allowedSigners := filepath.Join(t.TempDir(), "allowed_signers")
	signerEntry := fmt.Sprintf("dev@example.com %s %s\n", fields[0], fields[1])
	require.NoError(t, os.WriteFile(allowedSigners, []byte(signerEntry), 0o600))

	// #nosec G304 -- test file path under t.TempDir.
	payload, err := os.ReadFile(bufferFile)
	require.NoError(t, err)

	//nolint:gosec // test-only ssh-keygen verification under t.TempDir.
	cmd := exec.Command(
		"ssh-keygen",
		"-Y",
		"verify",
		"-f",
		allowedSigners,
		"-I",
		"dev@example.com",
		"-n",
		"git",
		"-s",
		bufferFile+".sig",
	)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ssh-keygen -Y verify failed: %s", string(out))
}
