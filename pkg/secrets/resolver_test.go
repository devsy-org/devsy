package secrets

import (
	"context"
	"errors"
	"testing"
)

const testResolverToken = "TOKEN"

type testSource struct {
	value string
	err   error
	calls int
}

func (s *testSource) Get(_ context.Context, name string) (ResolvedSecret, error) {
	s.calls++
	if s.err != nil {
		return ResolvedSecret{}, s.err
	}
	return ResolvedSecret{Name: name, Value: s.value, Sensitive: true}, nil
}

func TestResolverRoutesExplicitSource(t *testing.T) {
	r := NewResolver()
	s := &testSource{value: "secret"}
	if err := r.Register(testProjectSource, SOPSFormatter, s); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(
		context.Background(),
		SecretRef{Type: SOPSFormatter, Source: testProjectSource, Name: testResolverToken},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "secret" || got.Source != testProjectSource || got.Name != testResolverToken {
		t.Fatalf("unexpected resolved secret: %#v", got)
	}
}

func TestResolverDoesNotFallback(t *testing.T) {
	r := NewResolver()
	local := &testSource{value: LocalSourceName}
	if err := r.Register(LocalSourceName, LocalSourceName, local); err != nil {
		t.Fatal(err)
	}
	_, err := r.Resolve(
		context.Background(),
		SecretRef{Type: SOPSFormatter, Source: "missing", Name: testResolverToken},
	)
	if err == nil {
		t.Fatal("expected missing source error")
	}
	if local.calls != 0 {
		t.Fatalf("local source was called %d times", local.calls)
	}
}

func TestResolverWrapsSourceError(t *testing.T) {
	r := NewResolver()
	s := &testSource{err: ErrSecretNotFound}
	if err := r.Register(testProjectSource, SOPSFormatter, s); err != nil {
		t.Fatal(err)
	}
	_, err := r.Resolve(
		context.Background(),
		SecretRef{Type: SOPSFormatter, Source: testProjectSource, Name: testResolverToken},
	)
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
}
