// Generates a signed JSON Web Token (JWT) for the Devsy GitHub App.
//
// GitHub App authentication requires a JWT signed with the app's RSA private
// key using the RS256 algorithm, carrying the registered claims:
//
//   - iss: the GitHub App client ID (prefer the client ID string; the numeric
//     app ID is accepted as a legacy fallback)
//   - iat: issued-at time, backdated 60 seconds to tolerate clock drift
//   - exp: expiration, 10 minutes after JWT generation (GitHub's maximum)
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

const (
	maxExpiration     = 10 * time.Minute
	issuedAtSkew      = 60 * time.Second
	envClientID       = "DEVSY_GITHUB_APP_CLIENT_ID"
	envAppID          = "DEVSY_GITHUB_APP_ID"
	envPrivateKey     = "DEVSY_GITHUB_APP_PRIVATE_KEY"
	envPrivateKeyPath = "DEVSY_GITHUB_APP_PRIVATE_KEY_PATH"
)

func main() {
	clientIDDefault := os.Getenv(envClientID)
	if clientIDDefault == "" {
		clientIDDefault = os.Getenv(envAppID)
	}
	clientID := flag.String("app-id", clientIDDefault,
		"GitHub App client ID (defaults to $"+envClientID+", then $"+envAppID+")")
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
		return fmt.Errorf("a GitHub App client ID is required: pass --app-id or set %s or %s",
			envClientID, envAppID)
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

func generateJWT(clientID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    clientID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-issuedAtSkew)),
		ExpiresAt: jwt.NewNumericDate(now.Add(maxExpiration)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

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
