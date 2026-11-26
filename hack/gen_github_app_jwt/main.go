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
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/hack/githubapp"
)

const (
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

	key, err := githubapp.LoadPrivateKey(privateKeyPath, privateKeyVar)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	token, err := githubapp.GenerateJWT(clientID, key, time.Now())
	if err != nil {
		return fmt.Errorf("generate jwt: %w", err)
	}

	fmt.Println(token)
	return nil
}
