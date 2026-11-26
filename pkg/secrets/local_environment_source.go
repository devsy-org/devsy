package secrets

import (
	"context"

	"github.com/devsy-org/devsy/pkg/envstore"
)

type localEnvironmentSource struct {
	store   envstore.EnvStore
	context string
}

func (s *localEnvironmentSource) Get(_ context.Context, name string) (ResolvedSecret, error) {
	value, err := s.store.Get(s.context, name)
	if err != nil {
		return ResolvedSecret{}, err
	}
	return ResolvedSecret{Name: name, Value: value, Source: LocalSourceName, Sensitive: false}, nil
}
