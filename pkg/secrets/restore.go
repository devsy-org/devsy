package secrets

import (
	"errors"
	"strings"
)

// SecretRestorer compensates a synchronous managed-value deletion using its
// captured metadata and plaintext. It preserves the recorded owning backend.
type SecretRestorer interface {
	Restore(meta SecretMeta, value string) error
}

var _ SecretRestorer = (*secretOnlyStore)(nil)

type secretRestoreError struct{ cause error }

func (e *secretRestoreError) Error() string { return "cannot restore captured secret state" }
func (e *secretRestoreError) Unwrap() error { return e.cause }

// Restore restores captured secret state without evaluating backend preference.
// The caller must treat any error as failed compensation of its outer operation.
func (s *secretOnlyStore) Restore(meta SecretMeta, value string) (err error) {
	defer func() {
		if err != nil {
			err = &secretRestoreError{cause: err}
		}
	}()
	if err = validateRestoreMeta(meta); err != nil {
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return err
	}
	return s.restoreWithIndex(idx, meta, value)
}

func (s *secretOnlyStore) restoreWithIndex(idx *index, meta SecretMeta, value string) error {
	if current, exists := idx.get(meta.Context, meta.Name); exists {
		if !current.Sensitive() || current.Backend != meta.Backend {
			return errors.New("captured secret ownership conflicts with the catalog")
		}
	}
	// Successful secret deletion retains the file blob and its key source. A
	// restoration must never initialize or choose different protection material.
	b, err := s.backends.Open(meta.Backend, idx, BackendOpenExisting)
	if err != nil {
		return err
	}
	if err = b.set(backendKey(meta.Context, meta.Name), value); err != nil {
		return err
	}
	meta.Value = ""
	idx.put(meta)
	return s.saveIndex(idx)
}

func validateRestoreMeta(meta SecretMeta) error {
	if err := ValidateName(meta.Name); err != nil {
		return err
	}
	if strings.TrimSpace(meta.Context) == "" {
		return errors.New("captured secret context must not be empty")
	}
	if strings.ContainsAny(meta.Context, "\x00\r\n") {
		return errors.New("captured secret context contains control characters")
	}
	if meta.Value != "" {
		return errors.New("captured secret metadata must not contain plaintext")
	}
	switch meta.Kind {
	case "", KindSecret:
	default:
		return errors.New("captured metadata is not a secret")
	}
	return validateRestoreBackend(meta.Backend)
}

func validateRestoreBackend(kind Backend) error {
	switch kind {
	case BackendFile, BackendKeyring:
		return nil
	default:
		return errors.New("captured secret must have a known owning backend")
	}
}
