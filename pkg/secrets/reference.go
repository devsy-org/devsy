package secrets

import (
	"fmt"
	"strings"
)

const LocalSourceName = "local"

const qualifiedRefFormat = "TYPE:SOURCE/NAME"

// SecretRef identifies a named secret and the source instance that owns it.
// Unqualified references resolve from the local Devsy store. Qualified
// references use TYPE:SOURCE/NAME, for example sops:project/API_TOKEN.
type SecretRef struct {
	Type   string
	Source string
	Name   string
}

func ParseRef(value string) (SecretRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SecretRef{}, fmt.Errorf("invalid secret reference: value must not be empty")
	}
	if !strings.Contains(value, ":") {
		return parseLocalRef(value)
	}
	return parseQualifiedRef(value)
}

func parseLocalRef(value string) (SecretRef, error) {
	if err := ValidateName(value); err != nil {
		return SecretRef{}, err
	}
	return SecretRef{Type: LocalSourceName, Source: LocalSourceName, Name: value}, nil
}

func parseQualifiedRef(value string) (SecretRef, error) {
	typeName, rest, err := splitQualifiedType(value)
	if err != nil {
		return SecretRef{}, err
	}
	sourceName, name, err := splitQualifiedName(value, rest)
	if err != nil {
		return SecretRef{}, err
	}
	if err := ValidateSourceName(sourceName); err != nil {
		return SecretRef{}, fmt.Errorf("invalid secret reference %q: %w", value, err)
	}
	if err := ValidateName(name); err != nil {
		return SecretRef{}, fmt.Errorf("invalid secret reference %q: %w", value, err)
	}
	return SecretRef{Type: typeName, Source: sourceName, Name: name}, nil
}

func splitQualifiedType(value string) (string, string, error) {
	typeName, rest, ok := strings.Cut(value, ":")
	if !ok || typeName == "" || rest == "" {
		return "", "", invalidQualifiedRef(value)
	}
	return typeName, rest, nil
}

func splitQualifiedName(value, rest string) (string, string, error) {
	sourceName, name, ok := strings.Cut(rest, "/")
	if !ok || sourceName == "" || name == "" || strings.Contains(name, "/") {
		return "", "", invalidQualifiedRef(value)
	}
	return sourceName, name, nil
}

func invalidQualifiedRef(value string) error {
	return fmt.Errorf("invalid secret reference %q: expected %s", value, qualifiedRefFormat)
}

func (r SecretRef) String() string {
	if r.Source == LocalSourceName && (r.Type == "" || r.Type == LocalSourceName) {
		return r.Name
	}
	return r.Type + ":" + r.Source + "/" + r.Name
}
