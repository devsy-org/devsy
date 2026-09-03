package up

import (
	"context"
	"sort"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	secretspkg "github.com/devsy-org/devsy/pkg/secrets"
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

func localRef(name string) secretspkg.SecretRef {
	return secretspkg.SecretRef{
		Type:   secretspkg.LocalSourceName,
		Source: secretspkg.LocalSourceName,
		Name:   name,
	}
}

func sortedRequests(reqs []secretRequest) []secretRequest {
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].ref.String() < reqs[j].ref.String() })
	return reqs
}

func TestCollectSecretRequests_FlagsDefaultToEnv(t *testing.T) {
	got, err := collectSecretRequests(
		[]string{secretAPIKey, "DB_PW,target=DATABASE_PASSWORD"},
		testConfig(),
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{ref: localRef(secretAPIKey), target: secretAPIKey, mount: false},
		{ref: localRef("DB_PW"), target: "DATABASE_PASSWORD", mount: false},
	}, sortedRequests(got))
}

func TestCollectSecretRequests_SourceQualified(t *testing.T) {
	got, err := collectSecretRequests(
		[]string{"sops:project/API_KEY,type=mount,target=token"},
		testConfig(),
		nil,
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sops:project/API_KEY", got[0].ref.String())
	assert.Equal(t, "token", got[0].target)
	assert.True(t, got[0].mount)
}

func TestCollectSecretRequests_ProjectBindings(t *testing.T) {
	got, err := collectSecretRequests(nil, testConfig(), []string{"sops:project/API_KEY"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sops:project/API_KEY", got[0].ref.String())
	assert.Equal(t, "API_KEY", got[0].target)
}

func TestCollectSecretRequests_MountType(t *testing.T) {
	got, err := collectSecretRequests([]string{secretTLSKey + ",type=mount"}, testConfig(), nil)
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{ref: localRef(secretTLSKey), target: secretTLSKey, mount: true},
	}, got)
}

func TestCollectSecretRequests_ContextBindings(t *testing.T) {
	got, err := collectSecretRequests(nil, testConfig(secretToken, "SECRET_TWO"), nil)
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{ref: localRef("SECRET_TWO"), target: "SECRET_TWO", mount: false},
		{ref: localRef(secretToken), target: secretToken, mount: false},
	}, sortedRequests(got))
}

func TestCollectSecretRequests_FlagOverridesBinding(t *testing.T) {
	got, err := collectSecretRequests(
		[]string{"TOKEN,target=CI_TOKEN"},
		testConfig(secretToken),
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, []secretRequest{
		{ref: localRef(secretToken), target: "CI_TOKEN", mount: false},
	}, got)
}

func TestCollectSecretRequests_Empty(t *testing.T) {
	got, err := collectSecretRequests(nil, testConfig(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCollectSecretRequests_InvalidType(t *testing.T) {
	_, err := collectSecretRequests([]string{"A,type=bogus"}, testConfig(), nil)
	require.Error(t, err)
}

func TestCollectSecretRequests_InvalidOption(t *testing.T) {
	_, err := collectSecretRequests([]string{"A,bogus"}, testConfig(), nil)
	require.Error(t, err)
}

func TestCollectSecretRequests_DuplicateMountTargetRejected(t *testing.T) {
	_, err := collectSecretRequests(
		[]string{"A,type=mount,target=tok", "sops:project/B,type=mount,target=tok"},
		testConfig(),
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both mount to target")
}

type fixedSource struct {
	values    map[string]string
	sensitive bool
}

func (s fixedSource) Get(_ context.Context, name string) (secretspkg.ResolvedSecret, error) {
	return secretspkg.ResolvedSecret{Name: name, Value: s.values[name], Sensitive: s.sensitive}, nil
}

func TestApplyEnvVars_RejectsSensitiveSecret(t *testing.T) {
	cmd := &UpCmd{}
	cmd.EnvVars = []string{secretAPIKey}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("local", "local", fixedSource{
		values: map[string]string{secretAPIKey: "plaintext"}, sensitive: true,
	}))

	err := cmd.applyEnvVars(context.Background(), resolver)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a secret and cannot be passed with --env")
	assert.Empty(t, cmd.WorkspaceEnv, "no value must reach the ps-visible WorkspaceEnv")
}

func TestApplyEnvVars_AllowsNonSensitive(t *testing.T) {
	cmd := &UpCmd{}
	cmd.EnvVars = []string{"LOG_LEVEL=DEBUG_TARGET"}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("local", "local", fixedSource{
		values: map[string]string{"LOG_LEVEL": "debug"}, sensitive: false,
	}))

	err := cmd.applyEnvVars(context.Background(), resolver)
	require.NoError(t, err)
	assert.Equal(t, []string{"DEBUG_TARGET=debug"}, cmd.WorkspaceEnv)
}

func TestApplyBuildSecretsUsesUnqualifiedKeyAsID(t *testing.T) {
	cmd := &UpCmd{}
	cmd.BuildSecretNames = []string{"sops:project/NPM_TOKEN"}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("project", "sops", fixedSource{
		values: map[string]string{"NPM_TOKEN": "secret"}, sensitive: true,
	}))

	err := cmd.applyBuildSecrets(context.Background(), resolver)
	require.NoError(t, err)
	assert.Equal(t, []string{"NPM_TOKEN=secret"}, cmd.BuildSecrets)
}

func TestApplyBuildSecretsRejectsDuplicateIDs(t *testing.T) {
	cmd := &UpCmd{}
	cmd.BuildSecretNames = []string{"sops:project/NPM_TOKEN", "sops:other/NPM_TOKEN"}
	resolver := secretspkg.NewResolver()
	require.NoError(t, resolver.Register("project", "sops", fixedSource{
		values: map[string]string{"NPM_TOKEN": "secret-a"}, sensitive: true,
	}))
	require.NoError(t, resolver.Register("other", "sops", fixedSource{
		values: map[string]string{"NPM_TOKEN": "secret-b"}, sensitive: true,
	}))

	err := cmd.applyBuildSecrets(context.Background(), resolver)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both use BuildKit id \"NPM_TOKEN\"")
	assert.Empty(t, cmd.BuildSecrets)
}
