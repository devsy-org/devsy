package up

import (
	"sort"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	secretAPIKey = "API_KEY"
	secretToken  = "TOKEN"
	secretTLSKey = "TLS_KEY"
)

func testConfig(bound ...string) *config.Config {
	return &config.Config{
		DefaultContext: "default",
		Contexts: map[string]*config.ContextConfig{
			"default": {Secrets: bound},
		},
	}
}

func sortedRequests(reqs []secretRequest) []secretRequest {
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].name < reqs[j].name })
	return reqs
}

func TestCollectSecretRequests_FlagsDefaultToEnv(t *testing.T) {
	got, err := collectSecretRequests(
		[]string{secretAPIKey, "DB_PW,target=DATABASE_PASSWORD"},
		testConfig(),
	)
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{name: secretAPIKey, target: secretAPIKey, mount: false},
		{name: "DB_PW", target: "DATABASE_PASSWORD", mount: false},
	}, sortedRequests(got))
}

func TestCollectSecretRequests_MountType(t *testing.T) {
	got, err := collectSecretRequests([]string{secretTLSKey + ",type=mount"}, testConfig())
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{name: secretTLSKey, target: secretTLSKey, mount: true},
	}, got)
}

func TestCollectSecretRequests_MountWithTarget(t *testing.T) {
	got, err := collectSecretRequests(
		[]string{secretTLSKey + ",type=mount,target=tls.key"},
		testConfig(),
	)
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{name: secretTLSKey, target: "tls.key", mount: true},
	}, got)
}

func TestCollectSecretRequests_ContextBindings(t *testing.T) {
	got, err := collectSecretRequests(nil, testConfig(secretToken, "SECRET_TWO"))
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{name: "SECRET_TWO", target: "SECRET_TWO", mount: false},
		{name: secretToken, target: secretToken, mount: false},
	}, sortedRequests(got))
}

func TestCollectSecretRequests_FlagOverridesBinding(t *testing.T) {
	got, err := collectSecretRequests([]string{"TOKEN,target=CI_TOKEN"}, testConfig(secretToken))
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{name: secretToken, target: "CI_TOKEN", mount: false},
	}, got)
}

func TestCollectSecretRequests_Empty(t *testing.T) {
	got, err := collectSecretRequests(nil, testConfig())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCollectSecretRequests_InvalidType(t *testing.T) {
	_, err := collectSecretRequests([]string{"A,type=bogus"}, testConfig())
	require.Error(t, err)
}

func TestCollectSecretRequests_InvalidOption(t *testing.T) {
	_, err := collectSecretRequests([]string{"A,bogus"}, testConfig())
	require.Error(t, err)
}

func TestCollectSecretRequests_DuplicateMountTargetRejected(t *testing.T) {
	_, err := collectSecretRequests(
		[]string{"A,type=mount,target=tok", "B,type=mount,target=tok"},
		testConfig(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both mount to target")
}

func TestApplyEnvVars_RejectsSensitiveSecret(t *testing.T) {
	cmd := &UpCmd{}
	cmd.EnvVars = []string{secretAPIKey}
	get := func(string) (string, error) { return "plaintext", nil }
	isSensitive := func(string) (bool, error) { return true, nil }

	err := cmd.applyEnvVars(get, isSensitive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a secret and cannot be passed with --env")
	assert.Empty(t, cmd.WorkspaceEnv, "no value must reach the ps-visible WorkspaceEnv")
}

func TestApplyEnvVars_AllowsNonSensitive(t *testing.T) {
	cmd := &UpCmd{}
	cmd.EnvVars = []string{"LOG_LEVEL=DEBUG_TARGET"}
	get := func(string) (string, error) { return "debug", nil }
	isSensitive := func(string) (bool, error) { return false, nil }

	err := cmd.applyEnvVars(get, isSensitive)
	require.NoError(t, err)
	assert.Equal(t, []string{"DEBUG_TARGET=debug"}, cmd.WorkspaceEnv)
}
