// Package githubapp signs JSON Web Tokens for the Devsy GitHub App and loads
// the app's RSA private key. The hack/ tools that authenticate as the app share
// this code.
package githubapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	maxExpiration = 10 * time.Minute
	issuedAtSkew  = 60 * time.Second
)

// GenerateJWT returns an RS256-signed JWT for the GitHub App with the given
// client ID. iat is backdated by issuedAtSkew and exp is maxExpiration after now.
func GenerateJWT(clientID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	if clientID == "" {
		return "", errors.New("a GitHub App client ID is required")
	}
	claims := jwt.RegisteredClaims{
		Issuer:    clientID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-issuedAtSkew)),
		ExpiresAt: jwt.NewNumericDate(now.Add(maxExpiration)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// LoadPrivateKey parses the app's PEM private key. contents takes precedence
// over path; path is read from disk only when contents is empty.
func LoadPrivateKey(path, contents string) (*rsa.PrivateKey, error) {
	pemBytes, err := keyBytes(path, contents)
	if err != nil {
		return nil, err
	}

	if key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes); err == nil {
		return key, nil
	}

	return parsePEMBlock(pemBytes)
}

func keyBytes(path, contents string) ([]byte, error) {
	switch {
	case contents != "":
		return []byte(contents), nil
	case path != "":
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read private key file %q: %w", path, err)
		}
		return b, nil
	default:
		return nil, errors.New(
			"no private key provided: set DEVSY_GITHUB_APP_PRIVATE_KEY or DEVSY_GITHUB_APP_PRIVATE_KEY_PATH",
		)
	}
}

func parsePEMBlock(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("failed to decode PEM block from private key")
	}

	var parsedKey any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsedKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		parsedKey, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	key, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not an RSA key")
	}

	return key, nil
}
