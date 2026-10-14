package secrets

import "errors"

type SecretAvailability string

const (
	SecretAvailable          SecretAvailability = "available"
	SecretLocked             SecretAvailability = "locked"
	SecretMissing            SecretAvailability = "missing"
	SecretBackendUnavailable SecretAvailability = "backend_unavailable"
	SecretStateUnknown       SecretAvailability = "unknown"
)

type SecretInspection struct {
	Meta         SecretMeta         `json:"meta"`
	Availability SecretAvailability `json:"availability"`
	ReasonCode   string             `json:"reasonCode,omitempty"`
}
type SecretCatalog interface {
	Meta(contextName, name string) (SecretMeta, error)
	ListMeta(contextName string) ([]SecretMeta, error)
	Inspect(contextName string) ([]SecretInspection, error)
}
type SecretStore interface {
	SecretCatalog
	Set(contextName, name, value string) error
	Get(contextName, name string) (string, error)
	Delete(contextName, name string) error
}

// secretOnlyStore provides the secret domain API during the legacy Store API
// transition. Plaintext environment values have their own package/store.
type secretOnlyStore struct{ *localStore }

func (s *secretOnlyStore) Set(contextName, name, value string) error {
	return s.localStore.Set(contextName, name, value, KindSecret)
}

func (s *localStore) ListMeta(contextName string) ([]SecretMeta, error) {
	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return nil, err
	}
	result := make([]SecretMeta, 0)
	for _, meta := range idx.list(contextName) {
		if meta.Sensitive() {
			meta.Value = ""
			result = append(result, meta)
		}
	}
	return result, nil
}

type inspectionBackend struct {
	backend backend
	values  map[string]string
	err     error
}

func (s *localStore) Inspect(contextName string) ([]SecretInspection, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return nil, err
	}
	opened := map[Backend]inspectionBackend{}
	result := make([]SecretInspection, 0)
	for _, meta := range idx.list(contextName) {
		if meta.Sensitive() && meta.Backend != "" {
			if _, exists := opened[meta.Backend]; !exists {
				opened[meta.Backend] = s.openInspectionBackend(idx, meta.Backend)
			}
		}
		result = append(result, inspectEntry(meta, opened[meta.Backend]))
	}
	return result, nil
}

func (s *localStore) openInspectionBackend(idx *index, kind Backend) inspectionBackend {
	b, err := s.backends.Open(kind, idx, BackendInspect)
	state := inspectionBackend{backend: b, err: err}
	if err == nil {
		if fb, ok := b.(*fileBackend); ok {
			state.values, state.err = fb.load()
		}
	}
	return state
}

func inspectEntry(meta SecretMeta, state inspectionBackend) SecretInspection {
	if !meta.Sensitive() {
		return SecretInspection{Meta: meta, Availability: SecretAvailable}
	}
	meta.Value = ""
	if meta.Backend == "" {
		return SecretInspection{
			Meta:         meta,
			Availability: SecretStateUnknown,
			ReasonCode:   "ownership_unknown",
		}
	}
	availability, reason := inspectionAvailability(
		state.valueError(backendKey(meta.Context, meta.Name)),
	)
	return SecretInspection{Meta: meta, Availability: availability, ReasonCode: reason}
}

func (b inspectionBackend) valueError(key string) error {
	if b.err != nil {
		return b.err
	}
	if _, ok := b.backend.(*fileBackend); ok {
		if _, found := b.values[key]; !found {
			return ErrSecretNotFound
		}
		return nil
	}
	_, err := b.backend.get(key)
	return err
}

func inspectionAvailability(err error) (SecretAvailability, string) {
	switch {
	case err == nil:
		return SecretAvailable, ""
	case errors.Is(err, ErrUnlockRequired):
		return SecretLocked, "unlock_required"
	case errors.Is(err, ErrUnlockFailed):
		return SecretLocked, "unlock_failed"
	case errors.Is(err, ErrSecretNotFound):
		return SecretMissing, "secret_not_found"
	case errors.Is(err, ErrStoreCorrupt):
		return SecretStateUnknown, "store_corrupt"
	default:
		return SecretBackendUnavailable, "backend_unavailable"
	}
}

func (s *secretOnlyStore) Meta(contextName, name string) (SecretMeta, error) {
	meta, err := s.localStore.Meta(contextName, name)
	if err != nil {
		return SecretMeta{}, err
	}
	if !meta.Sensitive() {
		return SecretMeta{}, ErrSecretNotFound
	}
	return meta, nil
}

func (s *secretOnlyStore) Get(contextName, name string) (string, error) {
	if _, err := s.Meta(contextName, name); err != nil {
		return "", err
	}
	return s.localStore.Get(contextName, name)
}

func (s *secretOnlyStore) Delete(contextName, name string) error {
	meta, err := s.localStore.Meta(contextName, name)
	if errors.Is(err, ErrSecretNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !meta.Sensitive() {
		return ErrSecretNotFound
	}
	return s.localStore.Delete(contextName, name)
}

func (s *secretOnlyStore) Inspect(contextName string) ([]SecretInspection, error) {
	entries, err := s.localStore.Inspect(contextName)
	if err != nil {
		return nil, err
	}
	result := make([]SecretInspection, 0, len(entries))
	for _, entry := range entries {
		if entry.Meta.Sensitive() {
			result = append(result, entry)
		}
	}
	return result, nil
}
