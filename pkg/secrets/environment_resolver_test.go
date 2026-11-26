package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentResolutionNeverFallsBackToSecretSource(t *testing.T) {
	called := false
	resolver := NewResolver()
	require.NoError(
		t,
		resolver.Register(
			LocalSourceName,
			LocalSourceName,
			environmentSourceFunc(func(context.Context, string) (ResolvedSecret, error) {
				called = true
				return ResolvedSecret{Value: "private", Sensitive: true}, nil
			}),
		),
	)
	_, err := resolver.ResolveEnvironment(
		t.Context(),
		SecretRef{Source: LocalSourceName, Name: testResolverToken},
	)
	require.Error(t, err)
	require.False(t, called)
}

type environmentSourceFunc func(context.Context, string) (ResolvedSecret, error)

func (f environmentSourceFunc) Get(ctx context.Context, name string) (ResolvedSecret, error) {
	return f(ctx, name)
}
