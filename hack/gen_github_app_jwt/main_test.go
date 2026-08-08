package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return key, pemBytes
}

func TestGenerateJWT(t *testing.T) {
	clientID := "123456"
	now := time.Now()
	key, _ := generateTestKey(t)

	tokenStr, err := generateJWT(clientID, key, now)
	if err != nil {
		t.Fatalf("generateJWT: %v", err)
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

func parseAndVerifyClaims(t *testing.T, tokenStr string, pub *rsa.PublicKey) *jwt.RegisteredClaims {
	t.Helper()
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
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

func TestLoadPrivateKeyFromEnv(t *testing.T) {
	_, pemBytes := generateTestKey(t)

	key, err := loadPrivateKey("", string(pemBytes))
	if err != nil {
		t.Fatalf("loadPrivateKey from contents: %v", err)
	}
	if key == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestLoadPrivateKeyMissing(t *testing.T) {
	if _, err := loadPrivateKey("", ""); err == nil {
		t.Fatal("expected error when no key is provided")
	}
}

func TestLoadPrivateKeyInvalid(t *testing.T) {
	if _, err := loadPrivateKey("", "not a pem"); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}
