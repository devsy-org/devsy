package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

func rsaPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der := x509.MarshalPKCS1PrivateKey(key)
	enc := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	if !strings.HasPrefix(string(enc), "-----BEGIN") {
		t.Fatalf("unexpected pem: %s", enc)
	}
	return enc
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func parseAndVerifyClaims(t *testing.T, tokenStr string, pub *rsa.PublicKey) *jwt.RegisteredClaims {
	t.Helper()
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			t.Fatalf("unexpected signing method: %v", token.Header["alg"])
		}
		return pub, nil
	})
	if err != nil {
		t.Fatalf("parse jwt: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("token is not valid")
	}
	return claims
}

func assertClaimTimes(t *testing.T, claims *jwt.RegisteredClaims, now time.Time) {
	t.Helper()
	expectedIAT := now.Add(-issuedAtSkew).Truncate(time.Second)
	if got := claims.IssuedAt.Truncate(time.Second); !got.Equal(expectedIAT) {
		t.Fatalf("expected iat %v, got %v", expectedIAT, got)
	}

	expectedExp := now.Add(maxExpiration).Truncate(time.Second)
	if got := claims.ExpiresAt.Truncate(time.Second); !got.Equal(expectedExp) {
		t.Fatalf("expected exp %v, got %v", expectedExp, got)
	}
}

func TestGenerateJWT(t *testing.T) {
	clientID := "123456"
	now := time.Now()
	key := testKey(t)

	tokenStr, err := GenerateJWT(clientID, key, now)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	claims := parseAndVerifyClaims(t, tokenStr, &key.PublicKey)

	if claims.Issuer != clientID {
		t.Fatalf("expected iss %q, got %q", clientID, claims.Issuer)
	}

	assertClaimTimes(t, claims, now)
}

func TestGenerateJWTMissingID(t *testing.T) {
	if _, err := GenerateJWT("", testKey(t), time.Now()); err == nil {
		t.Fatal("expected error for missing client id")
	}
}

func TestLoadPrivateKeyFromContents(t *testing.T) {
	key := testKey(t)
	loaded, err := LoadPrivateKey("", string(rsaPEM(t, key)))
	if err != nil {
		t.Fatalf("LoadPrivateKey from PKCS1 contents: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil key")
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Error("loaded PKCS1 key does not match")
	}
}

func TestLoadPrivateKeyPKCS8FromContents(t *testing.T) {
	key := testKey(t)
	loaded, err := LoadPrivateKey("", string(pkcs8PEM(t, key)))
	if err != nil {
		t.Fatalf("LoadPrivateKey from PKCS8 contents: %v", err)
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Error("loaded PKCS8 key does not match")
	}
}

func TestLoadPrivateKeyFromPath(t *testing.T) {
	key := testKey(t)
	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, rsaPEM(t, key), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	loaded, err := LoadPrivateKey(path, "")
	if err != nil {
		t.Fatalf("LoadPrivateKey from path: %v", err)
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Error("loaded key from path does not match")
	}
}

func TestLoadPrivateKeyContentsTakePrecedenceOverPath(t *testing.T) {
	key := testKey(t)
	other := testKey(t)
	path := filepath.Join(t.TempDir(), "other.pem")
	if err := os.WriteFile(path, rsaPEM(t, other), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	loaded, err := LoadPrivateKey(path, string(rsaPEM(t, key)))
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Error("expected key from contents, got key from path")
	}
}

func TestLoadPrivateKeyMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.pem")
	if _, err := LoadPrivateKey(path, ""); err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestLoadPrivateKeyMissing(t *testing.T) {
	if _, err := LoadPrivateKey("", ""); err == nil {
		t.Fatal("expected error when no key is provided")
	}
}

func TestLoadPrivateKeyInvalid(t *testing.T) {
	if _, err := LoadPrivateKey("", "not a pem"); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}
