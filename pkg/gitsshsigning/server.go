package gitsshsigning

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const defaultSigningNamespace = "git"

// GitSSHSignatureRequest describes an SSH signature request forwarded from a workspace container.
type GitSSHSignatureRequest struct {
	Content   string
	KeyPath   string
	PublicKey string // Public key content; when set, written to a temp file for ssh-keygen.
	Namespace string `json:",omitempty"`
}

// GitSSHSignatureResponse holds the raw SSH signature produced by ssh-keygen.
type GitSSHSignatureResponse struct {
	Signature []byte
}

// Sign signs the content using the requested key and returns the signature.
func (req *GitSSHSignatureRequest) Sign() (*GitSSHSignatureResponse, error) {
	return req.SignWithHostKey("")
}

// SignWithHostKey signs the content, preferring a matching host signing key file when available.
func (req *GitSSHSignatureRequest) SignWithHostKey(
	hostSigningKey string,
) (*GitSSHSignatureResponse, error) {
	namespace, err := resolveSigningNamespace(req.Namespace)
	if err != nil {
		return nil, err
	}

	keyFile, cleanup, err := req.resolveKeyFile(hostSigningKey)
	if err != nil {
		return nil, fmt.Errorf("resolve signing key: %w", err)
	}
	defer cleanup()
	//nolint:gosec // keyFile is a controlled temp path or validated signing key path
	cmd := exec.Command(
		"ssh-keygen",
		"-Y",
		"sign",
		"-f",
		keyFile,
		"-n",
		namespace,
	)
	cmd.Stdin = strings.NewReader(req.Content)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to sign commit: %w, stderr: %s", err, stderr.String())
	}

	return &GitSSHSignatureResponse{
		Signature: out.Bytes(),
	}, nil
}

// resolveKeyFile returns the path to use for ssh-keygen -f and a cleanup function.
func (req *GitSSHSignatureRequest) resolveKeyFile(hostSigningKey string) (string, func(), error) {
	noop := func() {}
	hostKeyPath := ExpandSigningKeyPath(hostSigningKey)

	if req.PublicKey == "" {
		if regularFileExists(hostKeyPath) {
			return hostKeyPath, noop, nil
		}
		return "", noop, errors.New("no public key content or host signing key file")
	}

	if hostKeyPath != "" && hostFileMatchesPublicKey(hostKeyPath, req.PublicKey) {
		return hostKeyPath, noop, nil
	}

	return writeTempPublicKeyFile(req.PublicKey)
}

func writeTempPublicKeyFile(publicKey string) (string, func(), error) {
	noop := func() {}
	tmpFile, err := os.CreateTemp("", ".git_signing_key_*")
	if err != nil {
		return "", noop, fmt.Errorf("create temp key file: %w", err)
	}
	keyPath := tmpFile.Name()

	content := strings.TrimRight(publicKey, "\r\n") + "\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(keyPath)
		return "", noop, fmt.Errorf("write public key: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(keyPath)
		return "", noop, fmt.Errorf("close temp key file: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(keyPath)
	}
	return keyPath, cleanup, nil
}

func resolveSigningNamespace(raw string) (string, error) {
	if raw == "" || raw == defaultSigningNamespace {
		return defaultSigningNamespace, nil
	}
	return "", fmt.Errorf("invalid signing namespace %q", raw)
}
