package secrets

import (
	"context"
	"fmt"
	"regexp"
)

var sourceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

type registeredSource struct {
	typeName string
	source   Source
}

// Resolver routes a SecretRef to an explicitly registered source instance.
type Resolver struct {
	sources map[string]registeredSource
}

func NewResolver() *Resolver {
	return &Resolver{sources: map[string]registeredSource{}}
}

func ValidateSourceName(name string) error {
	if !sourceNamePattern.MatchString(name) {
		return fmt.Errorf(
			"invalid secret source name %q: must start with a letter and contain only letters, digits, underscores, and hyphens",
			name,
		)
	}
	return nil
}

func (r *Resolver) Register(name, typeName string, source Source) error {
	if err := ValidateSourceName(name); err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("secret source %q is nil", name)
	}
	if _, exists := r.sources[name]; exists {
		return fmt.Errorf("secret source %q already exists", name)
	}
	if name == LocalSourceName && typeName != LocalSourceName {
		return fmt.Errorf("secret source name %q is reserved", LocalSourceName)
	}
	r.sources[name] = registeredSource{typeName: typeName, source: source}
	return nil
}

func (r *Resolver) Resolve(ctx context.Context, ref SecretRef) (ResolvedSecret, error) {
	registered, ok := r.sources[ref.Source]
	if !ok {
		return ResolvedSecret{}, fmt.Errorf("secret source %q is not configured", ref.Source)
	}
	if ref.Type != "" && registered.typeName != "" && ref.Type != registered.typeName {
		return ResolvedSecret{}, fmt.Errorf(
			"secret source %q is type %q, not %q",
			ref.Source,
			registered.typeName,
			ref.Type,
		)
	}
	resolved, err := registered.source.Get(ctx, ref.Name)
	if err != nil {
		return ResolvedSecret{}, fmt.Errorf(
			"resolve secret %q from source %q: %w",
			ref.Name,
			ref.Source,
			err,
		)
	}
	if resolved.Source == "" {
		resolved.Source = ref.Source
	}
	if resolved.Name == "" {
		resolved.Name = ref.Name
	}
	return resolved, nil
}
