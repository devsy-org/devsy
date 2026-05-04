package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

const testOptionToken = "token"

type SecretOptionsTestSuite struct {
	suite.Suite
}

func TestSecretOptionsTestSuite(t *testing.T) {
	suite.Run(t, new(SecretOptionsTestSuite))
}

func (s *SecretOptionsTestSuite) TestSecretOptionDetected() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			"apiKey": {Type: optionTypeSecret},
			"name":   {Type: optionTypeString},
		},
	}
	s.True(IsSecretOption(cfg.Options["apiKey"]))
	s.False(IsSecretOption(cfg.Options["name"]))
}

func (s *SecretOptionsTestSuite) TestSecretResolvedFromUserOptions() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret},
		},
	}
	userOpts := map[string]any{testOptionToken: "user-provided-value"}

	resolved, err := ResolveSecretOptions(testFeatureID, cfg, userOpts, nil)
	s.NoError(err)
	s.Equal("user-provided-value", resolved[testOptionToken])
}

func (s *SecretOptionsTestSuite) TestSecretResolvedFromEnvVar() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret},
		},
	}

	safeFeatureID := getFeatureSafeID(testFeatureID)
	safeOptionID := getFeatureSafeID(testOptionToken)
	envVar := "DEVCONTAINER_FEATURE_SECRET_" + safeFeatureID + "_" + safeOptionID
	s.T().Setenv(envVar, "env-secret-value")

	resolved, err := ResolveSecretOptions(testFeatureID, cfg, map[string]any{}, nil)
	s.NoError(err)
	s.Equal("env-secret-value", resolved[testOptionToken])
}

func (s *SecretOptionsTestSuite) TestSecretResolvedFromSecretsFile() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			"apiKey": {Type: optionTypeSecret},
		},
	}

	secretsFile := filepath.Join(s.T().TempDir(), "secrets.json")
	content := `{"` + testFeatureID + `": {"apiKey": "file-secret-value"}}`
	err := os.WriteFile(secretsFile, []byte(content), 0o600)
	s.Require().NoError(err)

	opts := &SecretOptions{SecretsFile: secretsFile}
	resolved, err := ResolveSecretOptions(testFeatureID, cfg, map[string]any{}, opts)
	s.NoError(err)
	s.Equal("file-secret-value", resolved["apiKey"])
}

func (s *SecretOptionsTestSuite) TestSecretPrecedenceUserOverEnv() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret},
		},
	}

	safeFeatureID := getFeatureSafeID(testFeatureID)
	safeOptionID := getFeatureSafeID(testOptionToken)
	envVar := "DEVCONTAINER_FEATURE_SECRET_" + safeFeatureID + "_" + safeOptionID
	s.T().Setenv(envVar, "env-value")

	userOpts := map[string]any{testOptionToken: "user-value"}
	resolved, err := ResolveSecretOptions(testFeatureID, cfg, userOpts, nil)
	s.NoError(err)
	s.Equal("user-value", resolved[testOptionToken])
}

func (s *SecretOptionsTestSuite) TestSecretPrecedenceEnvOverFile() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret},
		},
	}

	safeFeatureID := getFeatureSafeID(testFeatureID)
	safeOptionID := getFeatureSafeID(testOptionToken)
	envVar := "DEVCONTAINER_FEATURE_SECRET_" + safeFeatureID + "_" + safeOptionID
	s.T().Setenv(envVar, "env-value")

	secretsFile := filepath.Join(s.T().TempDir(), "secrets.json")
	content := `{"` + testFeatureID + `": {"` + testOptionToken + `": "file-value"}}`
	err := os.WriteFile(secretsFile, []byte(content), 0o600)
	s.Require().NoError(err)

	opts := &SecretOptions{SecretsFile: secretsFile}
	resolved, err := ResolveSecretOptions(testFeatureID, cfg, map[string]any{}, opts)
	s.NoError(err)
	s.Equal("env-value", resolved[testOptionToken])
}

func (s *SecretOptionsTestSuite) TestSecretFallsBackToDefault() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret, Default: "default-secret"},
		},
	}

	resolved, err := ResolveSecretOptions(testFeatureID, cfg, map[string]any{}, nil)
	s.NoError(err)
	s.Equal("default-secret", resolved[testOptionToken])
}

func (s *SecretOptionsTestSuite) TestSecretMissingReturnsError() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret},
		},
	}

	_, err := ResolveSecretOptions(testFeatureID, cfg, map[string]any{}, nil)
	s.Error(err)
	s.Contains(err.Error(), "secret option")
	s.Contains(err.Error(), testOptionToken)
	s.Contains(err.Error(), "required but no value was provided")
	s.Contains(err.Error(), "DEVCONTAINER_FEATURE_SECRET_")
}

func (s *SecretOptionsTestSuite) TestSecretMaskingInOptions() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionToken: {Type: optionTypeSecret},
			testOptionName:  {Type: optionTypeString},
		},
	}
	options := []string{
		`TOKEN="my-secret-value"`,
		`VERSION="1.0"`,
	}

	masked := maskSecretOptions(cfg, options)
	s.Equal(`TOKEN="****"`, masked[0])
	s.Equal(`VERSION="1.0"`, masked[1])
}

func (s *SecretOptionsTestSuite) TestSecretMaskingNoSecrets() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {Type: optionTypeString},
		},
	}
	options := []string{`VERSION="1.0"`}

	masked := maskSecretOptions(cfg, options)
	s.Equal(options, masked)
}

func (s *SecretOptionsTestSuite) TestParseFeatureSecretsFileValid() {
	secretsFile := filepath.Join(s.T().TempDir(), "secrets.json")
	content := `{
		"ghcr.io/owner/feature:1": {"secret1": "value1", "secret2": "value2"},
		"ghcr.io/owner/other:2": {"key": "val"}
	}`
	err := os.WriteFile(secretsFile, []byte(content), 0o600)
	s.Require().NoError(err)

	data, err := parseFeatureSecretsFile(secretsFile)
	s.NoError(err)
	s.Equal("value1", data["ghcr.io/owner/feature:1"]["secret1"])
	s.Equal("value2", data["ghcr.io/owner/feature:1"]["secret2"])
	s.Equal("val", data["ghcr.io/owner/other:2"]["key"])
}

func (s *SecretOptionsTestSuite) TestParseFeatureSecretsFileMissing() {
	_, err := parseFeatureSecretsFile("/nonexistent/path/secrets.json")
	s.Error(err)
	s.Contains(err.Error(), "read feature secrets file")
}

func (s *SecretOptionsTestSuite) TestParseFeatureSecretsFileInvalidJSON() {
	secretsFile := filepath.Join(s.T().TempDir(), "secrets.json")
	err := os.WriteFile(secretsFile, []byte("not json"), 0o600)
	s.Require().NoError(err)

	_, err = parseFeatureSecretsFile(secretsFile)
	s.Error(err)
	s.Contains(err.Error(), "parse feature secrets file")
}

func (s *SecretOptionsTestSuite) TestNonSecretOptionsUnaffected() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {Type: optionTypeString},
			"install":      {Type: optionTypeBoolean},
		},
	}

	userOpts := map[string]any{testOptionName: "1.0", "install": "true"}
	resolved, err := ResolveSecretOptions(testFeatureID, cfg, userOpts, nil)
	s.NoError(err)
	s.Equal("1.0", resolved[testOptionName])
	s.Equal("true", resolved["install"])
}

func (s *SecretOptionsTestSuite) TestNilFeatureConfigReturnsUserOptions() {
	userOpts := map[string]any{"key": "val"}
	resolved, err := ResolveSecretOptions(testFeatureID, nil, userOpts, nil)
	s.NoError(err)
	s.Equal(userOpts, resolved)
}

func (s *SecretOptionsTestSuite) TestEmptyOptionsMapReturnsEarly() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{},
	}
	userOpts := map[string]any{"key": "val"}
	resolved, err := ResolveSecretOptions(testFeatureID, cfg, userOpts, nil)
	s.NoError(err)
	s.Equal(userOpts, resolved)
}
