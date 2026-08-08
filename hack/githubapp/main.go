// Generates a signed JSON Web Token (JWT) for the Devsy GitHub App.
//
// GitHub App authentication requires a JWT signed with the app's RSA private
// key using the RS256 algorithm, carrying the registered claims:
//
//   - iss: the GitHub App client ID (numeric app ID or client ID string)
//   - iat: issued-at time, backdated 60 seconds to tolerate clock drift
//   - exp: expiration, at most 10 minutes after iat (GitHub's maximum)
//
// See the GitHub docs on generating a JWT for a GitHub App:
// https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/
// generating-a-json-web-token-jwt-for-a-github-app
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// maxExpiration matches GitHub's documented maximum JWT lifetime of 10 minutes.
const maxExpiration = 10 * time.Minute

// issuedAtSkew backdates the issued-at claim to tolerate clock drift, mirroring
// the example scripts in GitHub's documentation.
const issuedAtSkew = 60 * time.Second

// Environment variable names mirror the GitHub Actions secrets used across the
// release and CI workflows, so the helper runs with no arguments in CI.
const (
	envAppID          = "DEVSY_GITHUB_APP_ID"
	envPrivateKey     = "DEVSY_GITHUB_APP_PRIVATE_KEY"
	envPrivateKeyPath = "DEVSY_GITHUB_APP_PRIVATE_KEY_PATH"
)

func main() {
	clientID := flag.String("app-id", os.Getenv(envAppID),
		"GitHub App client ID (defaults to $"+envAppID+")")
	privateKeyPath := flag.String("private-key", os.Getenv(envPrivateKeyPath),
		"path to the GitHub App PEM private key (defaults to $"+envPrivateKeyPath+")")
	privateKeyVar := flag.String("private-key-content", os.Getenv(envPrivateKey),
		"GitHub App PEM private key contents (defaults to $"+envPrivateKey+")")
	flag.Parse()

	if err := run(*clientID, *privateKeyPath, *privateKeyVar); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(clientID, privateKeyPath, privateKeyVar string) error {
	if clientID == "" {
		return fmt.Errorf("a GitHub App client ID is required: pass --app-id or set %s", envAppID)
	}

	key, err := loadPrivateKey(privateKeyPath, privateKeyVar)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	token, err := generateJWT(clientID, key, time.Now())
	if err != nil {
		return fmt.Errorf("generate jwt: %w", err)
	}

	fmt.Println(token)
	return nil
}

// generateJWT builds and signs a GitHub App JWT using RS256.
func generateJWT(clientID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    clientID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-issuedAtSkew)),
		ExpiresAt: jwt.NewNumericDate(now.Add(maxExpiration)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// loadPrivateKey resolves the RSA private key from either an inline PEM
// string (preferred, e.g. a secret injected into the environment) or a PEM
// file path.
func loadPrivateKey(path, contents string) (*rsa.PrivateKey, error) {
	pemBytes, err := keyBytes(path, contents)
	if err != nil {
		return nil, err
	}

	if key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes); err == nil {
		return key, nil
	}

	return parsePEMBlock(pemBytes)
}

// keyBytes selects the PEM bytes from the inline contents or a file path.
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
		return nil, fmt.Errorf(
			"no private key provided: set %s or pass --private-key/--private-key-content",
			envPrivateKey,
		)
	}
}

// parsePEMBlock falls back to PKCS8 / PKCS1 parsing for PEM blocks the jwt
// helper does not accept directly.
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
