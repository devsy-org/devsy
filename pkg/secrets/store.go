package secrets

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var ErrSecretNotFound = errors.New("secret not found")

// namePattern enforces a POSIX env identifier; a leading digit is rejected
// because such a name cannot be exported as an environment variable.
var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidateName(name string) error {
	if name == "" {
		return errors.New("secret name must not be empty")
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf(
			"invalid secret name %q: must start with a letter or underscore and "+
				"contain only letters, digits, and underscores",
			name,
		)
	}

	return nil
}

// Kind distinguishes secrets (values in the backend) from env vars (inline).
type Kind string

const (
	KindSecret Kind = "secret"
	KindEnv    Kind = "env"
)

type SecretMeta struct {
	Name     string    `json:"name"`
	Context  string    `json:"context"`
	Kind     Kind      `json:"kind"`
	Value    string    `json:"value,omitempty"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`

	Orphaned bool `json:"-"`
}

func (m SecretMeta) Sensitive() bool { return m.Kind == KindSecret }

type Store interface {
	Set(context, name, value string, kind Kind) error
	Get(context, name string) (string, error)
	Meta(context, name string) (SecretMeta, error)
	List(context string) ([]SecretMeta, error)
	Delete(context, name string) error
}

type backend interface {
	set(key, value string) error
	get(key string) (string, error) // returns ErrSecretNotFound when absent
	remove(key string) error        // idempotent
}

func backendKey(context, name string) string {
	return context + "/" + name
}
