package gitsshsigning

import (
	"bytes"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	maxKeyFileSize        = 64 * 1024
	base64Alphabet        = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	openSSHPrivateKeyType = "OPENSSH PRIVATE KEY"
	openSSHKeyV1Magic     = "openssh-key-v1\x00"
)

var sshPublicKeyPrefixes = [...]string{
	"ssh-",
	"ecdsa-sha2-",
	"sk-ssh-",
	"sk-ecdsa-",
}

type openSSHPrivateKeyHeader struct {
	CipherName string
	KdfName    string
	KdfOpts    string
	NumKeys    uint32
	PubKey     []byte
	Rest       []byte `ssh:"rest"`
}

func readRegularKeyFile(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("empty key path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("key path %q is not a regular file", path)
	}
	if info.Size() > maxKeyFileSize {
		return nil, fmt.Errorf("key file %q exceeds max size", path)
	}
	// #nosec G304 -- signing key path comes from git configuration
	return os.ReadFile(path)
}

func regularFileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func looksLikePrivateKey(data []byte) bool {
	return bytes.Contains(data, []byte("PRIVATE KEY-----")) ||
		bytes.HasPrefix(bytes.TrimSpace(data), []byte("PuTTY-User-Key-File-"))
}

func extractPublicKeyForRequest(certPath string) string {
	data, err := readRegularKeyFile(certPath)
	if err != nil {
		return ""
	}
	if pub, ok := publicKeyFromPrivateKeyBytes(data); ok {
		return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	}
	if looksLikePrivateKey(data) {
		pubData, pubErr := readRegularKeyFile(certPath + ".pub")
		if pubErr != nil {
			return ""
		}
		return parsePublicKeyLine(pubData)
	}
	return parsePublicKeyLine(data)
}

func publicKeyFromPrivateKeyBytes(data []byte) (ssh.PublicKey, bool) {
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer.PublicKey(), true
	}
	var pmErr *ssh.PassphraseMissingError
	if errors.As(err, &pmErr) && pmErr.PublicKey != nil {
		return pmErr.PublicKey, true
	}
	return parseOpenSSHV1CleartextPublicKey(data)
}

func parseOpenSSHV1CleartextPublicKey(data []byte) (ssh.PublicKey, bool) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != openSSHPrivateKeyType {
		return nil, false
	}
	body, ok := bytes.CutPrefix(block.Bytes, []byte(openSSHKeyV1Magic))
	if !ok {
		return nil, false
	}
	var hdr openSSHPrivateKeyHeader
	if err := ssh.Unmarshal(body, &hdr); err != nil || hdr.NumKeys != 1 {
		return nil, false
	}
	pub, err := ssh.ParsePublicKey(hdr.PubKey)
	if err != nil {
		return nil, false
	}
	return pub, true
}

func parsePublicKeyLine(data []byte) string {
	if looksLikePrivateKey(data) {
		return ""
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || strings.ContainsAny(trimmed, "\r\n") {
		return ""
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey(data); err == nil {
		return trimmed
	}
	if hasSSHPublicKeyPrefix(trimmed) {
		return trimmed
	}
	return ""
}

func hasSSHPublicKeyPrefix(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || !isBase64Token(fields[1]) {
		return false
	}
	for _, prefix := range sshPublicKeyPrefixes {
		if strings.HasPrefix(fields[0], prefix) && len(fields[0]) > len(prefix) {
			return true
		}
	}
	return false
}

func isBase64Token(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if strings.IndexByte(base64Alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}

func hostFileMatchesPublicKey(hostPath, publicKey string) bool {
	if hostPath == "" || publicKey == "" {
		return false
	}
	reqPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return false
	}
	hostPub, err := extractSSHPublicKeyFromFile(hostPath)
	if err != nil {
		return false
	}
	return bytes.Equal(hostPub.Marshal(), reqPub.Marshal())
}

func extractSSHPublicKeyFromFile(path string) (ssh.PublicKey, error) {
	data, err := readRegularKeyFile(path)
	if err != nil {
		return nil, err
	}
	if pub, ok := publicKeyFromPrivateKeyBytes(data); ok {
		return pub, nil
	}
	if looksLikePrivateKey(data) {
		return parseAdjacentPublicKeyFile(path + ".pub")
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	return pub, err
}

func parseAdjacentPublicKeyFile(pubPath string) (ssh.PublicKey, error) {
	pubData, err := readRegularKeyFile(pubPath)
	if err != nil {
		return nil, err
	}
	if looksLikePrivateKey(pubData) {
		return nil, errors.New("adjacent public key file contains private key")
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
	return pub, err
}
