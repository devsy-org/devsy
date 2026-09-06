package secrets

import (
	"context"
	"fmt"
)

// LocalSource adapts the existing Devsy Store to the generic source interface.
type LocalSource struct {
	store   Store
	context string
}

func NewLocalSource(store Store, contextName string) *LocalSource {
	return &LocalSource{store: store, context: contextName}
}

func (s *LocalSource) Get(_ context.Context, name string) (ResolvedSecret, error) {
	if s == nil || s.store == nil {
		return ResolvedSecret{}, fmt.Errorf("local secret source is not configured")
	}
	value, err := s.store.Get(s.context, name)
	if err != nil {
		return ResolvedSecret{}, err
	}
	meta, err := s.store.Meta(s.context, name)
	if err != nil {
		return ResolvedSecret{}, err
	}
	return ResolvedSecret{
		Name:      name,
		Value:     value,
		Sensitive: meta.Sensitive(),
		Source:    LocalSourceName,
	}, nil
}
