package gitsshsigning

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestParseSignatureResponse_NonJSONBody(t *testing.T) {
	body := []byte("get git ssh signature: permission denied")
	_, err := parseSignatureResponse(body)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get git ssh signature: permission denied")
}

func TestParseSignatureResponse_ValidJSON(t *testing.T) {
	sig := []byte("ssh-sig-data")
	response := &GitSSHSignatureResponse{Signature: sig}
	body, err := json.Marshal(response)
	require.NoError(t, err)

	result, err := parseSignatureResponse(body)
	require.NoError(t, err)
	assert.Equal(t, sig, result)
}

func TestRequestContentSignature_ServerError_PlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(
			w,
			"get git ssh signature: failed to sign commit: exit status 1",
			http.StatusInternalServerError,
		)
	}))
	defer server.Close()

	signatureServerURL = server.URL + "/git-ssh-signature"
	t.Cleanup(func() { signatureServerURL = "" })

	_, err := requestContentSignature(
		[]byte("commit content"),
		"/tmp/key.pub",
		defaultSigningNamespace,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sign commit")
	assert.NotContains(t, err.Error(), "invalid character")
}

func TestRequestContentSignature_ServerError_JSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "signing failed"})
	}))
	defer server.Close()

	signatureServerURL = server.URL + "/git-ssh-signature"
	t.Cleanup(func() { signatureServerURL = "" })

	_, err := requestContentSignature(
		[]byte("commit content"),
		"/tmp/key.pub",
		defaultSigningNamespace,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing failed")
}

func TestRequestContentSignature_Success(t *testing.T) {
	sig := []byte("-----BEGIN SSH SIGNATURE-----\ntest\n-----END SSH SIGNATURE-----")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(&GitSSHSignatureResponse{Signature: sig})
	}))
	defer server.Close()

	signatureServerURL = server.URL + "/git-ssh-signature"
	t.Cleanup(func() { signatureServerURL = "" })

	result, err := requestContentSignature(
		[]byte("commit content"),
		"/tmp/key.pub",
		defaultSigningNamespace,
	)
	require.NoError(t, err)
	assert.Equal(t, sig, result)
	assert.Contains(t, string(result), "BEGIN SSH SIGNATURE")
}

func TestRequestContentSignature_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer server.Close()

	signatureServerURL = server.URL + "/git-ssh-signature"
	t.Cleanup(func() { signatureServerURL = "" })

	_, err := requestContentSignature(
		[]byte("commit content"),
		"/tmp/key.pub",
		defaultSigningNamespace,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not json at all")
}

func TestCreateSignatureRequestBody_IncludesPublicKeyContent(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "test_key.pub")
	pubKeyContent := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@example.com"
	require.NoError(t, os.WriteFile(certPath, []byte(pubKeyContent), 0o600))

	body, err := createSignatureRequestBody(
		[]byte("commit content"),
		certPath,
		defaultSigningNamespace,
	)
	require.NoError(t, err)

	var req GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Equal(t, "commit content", req.Content)
	assert.Equal(t, certPath, req.KeyPath)
	assert.Equal(t, pubKeyContent, req.PublicKey)
}

func TestCreateSignatureRequestBody_MissingCertFile_OmitsPublicKey(t *testing.T) {
	body, err := createSignatureRequestBody(
		[]byte("commit content"),
		"/nonexistent/key.pub",
		defaultSigningNamespace,
	)
	require.NoError(t, err)

	var req GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Empty(t, req.PublicKey)
}

func TestGetCredentialsPort_DefaultWhenEnvUnset(t *testing.T) {
	t.Setenv(config.EnvCredentialsServerPort, "")
	port, err := getCredentialsPort()
	require.NoError(t, err)
	assert.Equal(t, 12049, port)
}

func TestGetCredentialsPort_UsesEnvOverride(t *testing.T) {
	t.Setenv(config.EnvCredentialsServerPort, "54321")
	port, err := getCredentialsPort()
	require.NoError(t, err)
	assert.Equal(t, 54321, port)
}

func TestGetCredentialsPort_InvalidValueReturnsError(t *testing.T) {
	t.Setenv(config.EnvCredentialsServerPort, "not-a-port")
	_, err := getCredentialsPort()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-port")
}

func TestGetSignatureURL_OverrideReturnedDirectly(t *testing.T) {
	signatureServerURL = "http://override.example/sign"
	t.Cleanup(func() { signatureServerURL = "" })

	url, err := getSignatureURL()
	require.NoError(t, err)
	assert.Equal(t, "http://override.example/sign", url)
}

func TestGetSignatureURL_BuiltFromDefaultPort(t *testing.T) {
	signatureServerURL = ""
	t.Setenv(config.EnvCredentialsServerPort, "")

	url, err := getSignatureURL()
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:12049/git-ssh-signature", url)
}

func TestGetSignatureURL_ErrorWhenPortInvalid(t *testing.T) {
	signatureServerURL = ""
	t.Setenv(config.EnvCredentialsServerPort, "abc")

	_, err := getSignatureURL()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "abc")
}

func TestCreateSignatureRequestBody_OpenSSHPrivateKey_ExtractsPublicKeyOnly(t *testing.T) {
	tmpHome := t.TempDir()
	setTestHome(t, tmpHome)

	keyPath := filepath.Join(tmpHome, testSSHDirName, testSigningKeyName)
	pubLine := generateEd25519TestKey(t, keyPath, "")

	body, err := createSignatureRequestBody([]byte("commit content"), keyPath, testCustomNamespace)
	require.NoError(t, err)
	assert.NotContains(t, string(body), testPrivateKeyMarker)

	var req GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Equal(t, keyPath, req.KeyPath)
	assert.Equal(t, testCustomNamespace, req.Namespace)
	assert.True(t, strings.HasPrefix(req.PublicKey, testEd25519Prefix))
	assert.NotContains(t, req.PublicKey, testPrivateKeyMarker)

	expectedPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine))
	require.NoError(t, err)
	gotPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	require.NoError(t, err)
	assert.Equal(t, expectedPub.Marshal(), gotPub.Marshal())
}

func TestCreateSignatureRequestBody_EncryptedPrivateKey_ExtractsPublicKeyOnly(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "encrypted_ed25519")
	pubLine := generateEd25519TestKey(t, keyPath, "secretpass")
	require.NoError(t, os.Remove(keyPath+".pub"))

	body, err := createSignatureRequestBody(
		[]byte("commit content"),
		keyPath,
		defaultSigningNamespace,
	)
	require.NoError(t, err)
	assert.NotContains(t, string(body), testPrivateKeyMarker)

	var req GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal(body, &req))
	assert.True(t, strings.HasPrefix(req.PublicKey, testEd25519Prefix))
	assert.NotContains(t, req.PublicKey, testPrivateKeyMarker)

	expectedPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine))
	require.NoError(t, err)
	gotPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	require.NoError(t, err)
	assert.Equal(t, expectedPub.Marshal(), gotPub.Marshal())
}

func TestCreateSignatureRequestBody_MalformedPrivateKeyStub_NeverLeaksPrivateKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "malformed_id_ed25519")
	stub := "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-private-material\n-----END OPENSSH PRIVATE KEY-----\n"
	require.NoError(t, os.WriteFile(keyPath, []byte(stub), 0o600))

	body, err := createSignatureRequestBody(
		[]byte("commit content"),
		keyPath,
		defaultSigningNamespace,
	)
	require.NoError(t, err)
	assert.NotContains(t, string(body), testPrivateKeyMarker)
	assert.NotContains(t, string(body), "secret-private-material")

	var req GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Empty(t, req.PublicKey)

	require.NoError(t, os.WriteFile(keyPath+".pub", []byte(testSyntheticPublicKey+"\n"), 0o600))

	bodyWithPub, err := createSignatureRequestBody(
		[]byte("commit content"),
		keyPath,
		defaultSigningNamespace,
	)
	require.NoError(t, err)
	assert.NotContains(t, string(bodyWithPub), testPrivateKeyMarker)
	require.NoError(t, json.Unmarshal(bodyWithPub, &req))
	assert.Equal(t, testSyntheticPublicKey, req.PublicKey)
}

func TestHandleGitSSHProgramCall_ForwardsNamespace(t *testing.T) {
	var receivedReq GitSSHSignatureRequest
	sig := []byte("-----BEGIN SSH SIGNATURE-----\nsig\n-----END SSH SIGNATURE-----\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedReq)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&GitSSHSignatureResponse{Signature: sig})
	}))
	defer server.Close()

	SetSignatureServerURL(server.URL + "/git-ssh-signature")
	t.Cleanup(func() { SetSignatureServerURL("") })

	dir := t.TempDir()
	certPath := filepath.Join(dir, "key.pub")
	require.NoError(t, os.WriteFile(certPath, []byte("ssh-ed25519 AAAAC3 test@example.com"), 0o600))
	bufferFile := filepath.Join(dir, "buffer")
	require.NoError(t, os.WriteFile(bufferFile, []byte("payload to sign"), 0o600))

	require.NoError(t, HandleGitSSHProgramCall(certPath, testCustomNamespace, bufferFile))
	assert.Equal(t, testCustomNamespace, receivedReq.Namespace)
	writtenSig, err := os.ReadFile(bufferFile + ".sig") // #nosec G304 -- test path from t.TempDir
	require.NoError(t, err)
	assert.Equal(t, sig, writtenSig)
}

func TestCreateSignatureRequestBody_UnencryptedFIDO2SKPrivateKeyWithoutPubFile(t *testing.T) {
	rawKeyPath := filepath.Join(t.TempDir(), "seed_ed25519")
	_ = generateEd25519TestKey(t, rawKeyPath, "")
	seedBytes, err := os.ReadFile(rawKeyPath) // #nosec G304 -- test path from t.TempDir
	require.NoError(t, err)
	signer, err := ssh.ParsePrivateKey(seedBytes)
	require.NoError(t, err)

	// Build an openssh-key-v1 unencrypted sk-ssh-ed25519@openssh.com key stub:
	// outer header carries a valid sk-ssh-ed25519@openssh.com public key blob,
	// while inner private payload uses sk-ssh-ed25519@openssh.com (unhandled by x/crypto/ssh.ParsePrivateKey).
	var edWire struct {
		KeyType string
		Pub     []byte
	}
	require.NoError(t, ssh.Unmarshal(signer.PublicKey().Marshal(), &edWire))
	skPubWire := ssh.Marshal(struct {
		KeyType string
		Pub     []byte
		App     string
	}{
		KeyType: "sk-ssh-ed25519@openssh.com",
		Pub:     edWire.Pub,
		App:     "ssh:",
	})
	hdrBytes := ssh.Marshal(openSSHPrivateKeyHeader{
		CipherName: "none",
		KdfName:    "none",
		KdfOpts:    "",
		NumKeys:    1,
		PubKey:     skPubWire,
		Rest:       []byte("unhandled-sk-private-section"),
	})
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  openSSHPrivateKeyType,
		Bytes: append([]byte(openSSHKeyV1Magic), hdrBytes...),
	})

	skKeyPath := filepath.Join(t.TempDir(), "id_ed25519_sk")
	require.NoError(t, os.WriteFile(skKeyPath, pemBytes, 0o600))

	body, err := createSignatureRequestBody(
		[]byte("commit content"),
		skKeyPath,
		defaultSigningNamespace,
	)
	require.NoError(t, err)
	assert.NotContains(t, string(body), testPrivateKeyMarker)

	var req GitSSHSignatureRequest
	require.NoError(t, json.Unmarshal(body, &req))
	assert.True(t, strings.HasPrefix(req.PublicKey, "sk-ssh-ed25519@openssh.com "))
}
