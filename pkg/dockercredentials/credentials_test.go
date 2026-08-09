package dockercredentials

import (
	"testing"

	"github.com/docker/cli/cli/config/types"
	"github.com/stretchr/testify/suite"
)

type CredentialsTestSuite struct {
	suite.Suite
}

func TestCredentialsSuite(t *testing.T) {
	suite.Run(t, new(CredentialsTestSuite))
}

func (s *CredentialsTestSuite) TestAuthTokenPrefersUsernameSecretPair() {
	creds := &Credentials{Username: "user", Secret: "pass"}
	s.Equal("user:pass", creds.AuthToken())
}

func (s *CredentialsTestSuite) TestAuthTokenFallsBackToSecretWhenNoUsername() {
	creds := &Credentials{Secret: "token-only"}
	s.Equal("token-only", creds.AuthToken())
}

func (s *CredentialsTestSuite) TestAuthTokenEmptyWhenBothMissing() {
	creds := &Credentials{}
	s.Equal("", creds.AuthToken())
}

func (s *CredentialsTestSuite) TestCredentialsFromAuthConfigUsesPasswordByDefault() {
	ac := types.AuthConfig{
		Username:      "user",
		Password:      "pass",
		ServerAddress: "ghcr.io",
	}
	creds := credentialsFromAuthConfig(ac, "ghcr.io")
	s.Equal("ghcr.io", creds.ServerURL)
	s.Equal("user", creds.Username)
	s.Equal("pass", creds.Secret)
}

func (s *CredentialsTestSuite) TestCredentialsFromAuthConfigPrefersIdentityToken() {
	ac := types.AuthConfig{
		Username:      "user",
		Password:      "pass",
		IdentityToken: "identity-token",
		ServerAddress: "registry.example.com",
	}
	creds := credentialsFromAuthConfig(ac, "registry.example.com")
	s.Equal("identity-token", creds.Secret)
}

func (s *CredentialsTestSuite) TestCredentialsFromAuthConfigEmptySecretWhenNothingSet() {
	creds := credentialsFromAuthConfig(types.AuthConfig{}, "registry.example.com")
	s.Equal("registry.example.com", creds.ServerURL)
	s.Empty(creds.Username)
	s.Empty(creds.Secret)
}

func (s *CredentialsTestSuite) TestFillFromContainerCredentialsReturnsExistingWhenPopulated() {
	ac := types.AuthConfig{Username: "user", Password: "pass"}
	result, err := fillFromContainerCredentials(ac, "ghcr.io")
	s.NoError(err)
	s.Equal(ac, result)
}
