package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

const IndexFileName = "secrets.yaml"

const EncryptedFileName = "secrets.enc"

const EnvPassphrase = "DEVSY_SECRETS_PASSPHRASE" // #nosec G101 -- env var name, not a credential.

const EnvBackend = "DEVSY_SECRETS_BACKEND"

type Backend string

const (
	BackendAuto    Backend = "auto"
	BackendKeyring Backend = "keyring"
	BackendFile    Backend = "file"
)

type localStore struct {
	preference Backend
	backends   backendRegistry
	indexPath  string
	now        func() time.Time
	keySource  keySource
}

func NewStoreForConfig(devsyConfig *config.Config) (Store, error) {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(configPath)
	indexPath := filepath.Join(dir, IndexFileName)

	return newLocalStoreWithRegistry(resolveBackend(devsyConfig), indexPath,
		newSystemBackendRegistry(dir)), nil
}

func resolveBackend(devsyConfig *config.Config) Backend {
	if b := normalizeBackend(os.Getenv(EnvBackend)); b != "" {
		return b
	}
	if devsyConfig != nil {
		configured := devsyConfig.ContextOption(config.ContextOptionSecretsBackend)
		if b := normalizeBackend(configured); b != "" {
			return b
		}
	}

	return BackendAuto
}

func normalizeBackend(value string) Backend {
	switch Backend(value) {
	case BackendKeyring, BackendFile, BackendAuto:
		return Backend(value)
	default:
		return ""
	}
}

func newLocalStore(b backend, indexPath string) *localStore {
	return newLocalStoreWithRegistry(BackendKeyring, indexPath, fixedBackendRegistry{b: b})
}

func newLocalStoreWithRegistry(
	preference Backend,
	indexPath string,
	backends backendRegistry,
) *localStore {
	return &localStore{
		preference: preference,
		backends:   backends,
		indexPath:  indexPath,
		now:        time.Now,
	}
}

func (s *localStore) Set(context, name, value string, kind Kind) error {
	if err := ValidateName(name); err != nil {
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
	s.repairLegacyOwnership(idx)

	meta, exists := idx.get(context, name)
	if !exists {
		meta = SecretMeta{Name: name, Context: context, Created: s.now().UTC()}
	}
	wasSensitive := exists && meta.Sensitive()
	meta.Kind = kind
	meta.Value = value

	if err := s.persistValue(idx, &meta, value, wasSensitive); err != nil {
		return err
	}
	if meta.Sensitive() {
		if err := s.checkKeySource(idx); err != nil {
			return err
		}
		meta.Value = ""
	} else {
		meta.Backend = ""
	}

	idx.put(meta)
	return idx.save()
}

func (s *localStore) Get(context, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}

	idx, err := s.loadRepaired()
	if err != nil {
		return "", err
	}

	meta, ok := idx.get(context, name)
	if !ok {
		return "", ErrSecretNotFound
	}

	var value string
	if meta.Sensitive() {
		if err := s.checkKeySource(idx); err != nil {
			return "", err
		}
		if meta.Backend == "" {
			return "", unownedSecretError(context, name)
		}
		b, err := s.backends.Open(meta.Backend, idx, false)
		if err != nil {
			return "", err
		}
		if value, err = b.get(backendKey(context, name)); err != nil {
			return "", err
		}
	} else {
		value = meta.Value
	}

	s.touchLastUsed(context, name)

	return value, nil
}

func (s *localStore) Delete(context, name string) error {
	if err := ValidateName(name); err != nil {
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
	s.repairLegacyOwnership(idx)

	if meta, ok := idx.get(context, name); ok && meta.Sensitive() {
		if meta.Backend == "" {
			s.removeFromProbeableBackends(idx, backendKey(context, name))
		} else {
			b, openErr := s.backends.Open(meta.Backend, idx, false)
			if openErr != nil {
				return openErr
			}
			if err := b.remove(backendKey(context, name)); err != nil {
				return err
			}
		}
	}
	idx.remove(context, name)

	return idx.save()
}

func (s *localStore) Meta(context, name string) (SecretMeta, error) {
	idx, err := s.loadRepaired()
	if err != nil {
		return SecretMeta{}, err
	}
	meta, ok := idx.get(context, name)
	if !ok {
		return SecretMeta{}, ErrSecretNotFound
	}
	meta.Value = ""

	return meta, nil
}

// List returns the context's entries, flagging sensitive entries whose owned
// backend value is missing.
func (s *localStore) List(context string) ([]SecretMeta, error) {
	idx, err := s.loadRepaired()
	if err != nil {
		return nil, err
	}

	entries := idx.list(context)
	for i := range entries {
		if !entries[i].Sensitive() {
			continue
		}
		meta := entries[i]
		if meta.Backend == "" {
			entries[i].Orphaned = true
			continue
		}
		b, err := s.backends.Open(meta.Backend, idx, false)
		if err != nil {
			return nil, err
		}
		_, err = b.get(backendKey(context, entries[i].Name))
		if errors.Is(err, ErrSecretNotFound) {
			entries[i].Orphaned = true
		} else if err != nil {
			return nil, err
		}
	}

	return entries, nil
}

// lock serializes store read-modify-write. NOT reentrant: a lock-holding method
// must never call another locking method (see acquireFlock).
func (s *localStore) lock() (func(), error) {
	return acquireFlock(filepath.Dir(s.indexPath), IndexFileName+".lock")
}

func (s *localStore) touchLastUsed(context, name string) {
	unlock, err := s.lock()
	if err != nil {
		return
	}
	defer unlock()

	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return
	}
	meta, ok := idx.get(context, name)
	if !ok {
		return
	}
	meta.LastUsed = s.now().UTC()
	idx.put(meta)
	_ = idx.save()
}

func (s *localStore) persistValue(
	idx *index, meta *SecretMeta, value string, wasSensitive bool,
) error {
	if meta.Sensitive() {
		return s.persistSensitive(idx, meta, value, !wasSensitive)
	}
	if wasSensitive {
		return s.removeSensitive(idx, meta)
	}
	return nil
}

func (s *localStore) persistSensitive(
	idx *index, meta *SecretMeta, value string, create bool,
) error {
	if meta.Backend == "" {
		resolved, err := s.backends.ResolveForNewSecret(s.preference, idx)
		if err != nil {
			return err
		}
		meta.Backend = resolved
		create = true
	}
	b, err := s.backends.Open(meta.Backend, idx, create)
	if err != nil {
		return err
	}
	if err := b.set(backendKey(meta.Context, meta.Name), value); err != nil {
		return err
	}
	if s.keySource != "" {
		idx.data.KeySource = string(s.keySource)
	}
	return nil
}

func (s *localStore) removeSensitive(idx *index, meta *SecretMeta) error {
	if err := s.checkKeySource(idx); err != nil {
		return err
	}
	if meta.Backend == "" {
		s.removeFromProbeableBackends(idx, backendKey(meta.Context, meta.Name))
		return nil
	}
	b, err := s.backends.Open(meta.Backend, idx, false)
	if err != nil {
		return err
	}
	return b.remove(backendKey(meta.Context, meta.Name))
}

func (s *localStore) checkKeySource(idx *index) error {
	if s.keySource == "" || idx.data.KeySource == "" || idx.data.KeySource == string(s.keySource) {
		return nil
	}
	return fmt.Errorf(
		"secrets were encrypted with the %q key source but the current source is %q; "+
			"restore the original DEVSY_SECRETS_PASSPHRASE (or unset it) or re-create the secrets",
		idx.data.KeySource,
		s.keySource,
	)
}

func (s *localStore) loadRepaired() (*index, error) {
	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return nil, err
	}
	if s.repairLegacyOwnership(idx) {
		s.persistRepairs()
	}
	return idx, nil
}

func (s *localStore) persistRepairs() {
	unlock, err := s.lock()
	if err != nil {
		return
	}
	defer unlock()

	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return
	}
	if s.repairLegacyOwnership(idx) {
		_ = idx.save()
	}
}

func (s *localStore) repairLegacyOwnership(idx *index) bool {
	repaired := false
	for context, entries := range idx.data.Contexts {
		for name, meta := range entries {
			if !meta.Sensitive() || meta.Backend != "" {
				continue
			}
			owner, proven := s.provenOwner(idx, backendKey(context, name))
			if !proven {
				continue
			}
			meta.Backend = owner
			entries[name] = meta
			if owner == BackendFile && idx.data.KeySource == "" {
				idx.data.KeySource = string(keySourcePassphrase)
			}
			repaired = true
		}
	}
	return repaired
}

func (s *localStore) provenOwner(idx *index, key string) (Backend, bool) {
	var found []Backend
	for _, kind := range []Backend{BackendKeyring, BackendFile} {
		present, _ := s.backends.Probe(kind, idx, key)
		if present {
			found = append(found, kind)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

func (s *localStore) removeFromProbeableBackends(idx *index, key string) {
	for _, kind := range []Backend{BackendKeyring, BackendFile} {
		b, err := s.backends.Open(kind, idx, false)
		if err != nil {
			continue
		}
		_ = b.remove(key)
	}
}

func unownedSecretError(context, name string) error {
	return fmt.Errorf(
		"secret %s/%s has no proven owning backend and its value could not be "+
			"located; set it again with `devsy secret set %s` or remove it with "+
			"`devsy secret delete %s`",
		context,
		name,
		name,
		name,
	)
}
