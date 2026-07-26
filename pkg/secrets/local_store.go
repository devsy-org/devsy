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
	backend   backend
	indexPath string
	now       func() time.Time

	// keySource is empty for the keyring backend; checked against the index so a
	// key-source change is a clear error rather than a decryption failure.
	keySource keySource
}

func NewStoreForConfig(devsyConfig *config.Config) (Store, error) {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(configPath)
	indexPath := filepath.Join(dir, IndexFileName)

	switch resolveBackend(devsyConfig) {
	case BackendKeyring:
		return newLocalStore(keyringBackend{}, indexPath), nil
	case BackendFile:
		return newFileStore(dir, indexPath)
	default:
		if keyringAvailable() {
			return newLocalStore(keyringBackend{}, indexPath), nil
		}

		return newFileStore(dir, indexPath)
	}
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

func newFileStore(dir, indexPath string) (Store, error) {
	key, err := resolveFileKey(dir, os.Getenv(EnvPassphrase))
	if err != nil {
		return nil, err
	}

	fb := newFileBackend(filepath.Join(dir, EncryptedFileName), key)
	s := newLocalStore(fb, indexPath)
	s.keySource = key.source

	return s, nil
}

func newLocalStore(b backend, indexPath string) *localStore {
	return &localStore{backend: b, indexPath: indexPath, now: time.Now}
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
		meta.Value = ""
	}

	idx.put(meta)
	return idx.save()
}

func (s *localStore) Get(context, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}

	idx, err := loadIndex(s.indexPath)
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
		if value, err = s.backend.get(backendKey(context, name)); err != nil {
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

	if meta, ok := idx.get(context, name); ok && meta.Sensitive() {
		if err := s.checkKeySource(idx); err != nil {
			return err
		}
		if err := s.backend.remove(backendKey(context, name)); err != nil {
			return err
		}
	}
	idx.remove(context, name)

	return idx.save()
}

func (s *localStore) Meta(context, name string) (SecretMeta, error) {
	idx, err := loadIndex(s.indexPath)
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

// List returns the context's entries, flagging any sensitive entry whose backend
// value is missing (orphaned).
func (s *localStore) List(context string) ([]SecretMeta, error) {
	idx, err := loadIndex(s.indexPath)
	if err != nil {
		return nil, err
	}

	entries := idx.list(context)
	if !anySensitive(entries) {
		return entries, nil
	}
	if err := s.checkKeySource(idx); err != nil {
		return nil, err
	}

	for i := range entries {
		if !entries[i].Sensitive() {
			continue
		}
		orphaned, err := s.isOrphaned(context, entries[i].Name)
		if err != nil {
			return nil, err
		}
		entries[i].Orphaned = orphaned
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

func anySensitive(entries []SecretMeta) bool {
	for i := range entries {
		if entries[i].Sensitive() {
			return true
		}
	}
	return false
}

func (s *localStore) isOrphaned(context, name string) (bool, error) {
	_, err := s.backend.get(backendKey(context, name))
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, ErrSecretNotFound):
		return true, nil
	default:
		return false, fmt.Errorf("reconcile secret %q: %w", name, err)
	}
}

func (s *localStore) persistValue(
	idx *index, meta *SecretMeta, value string, wasSensitive bool,
) error {
	key := backendKey(meta.Context, meta.Name)
	if meta.Sensitive() || wasSensitive {
		if err := s.checkKeySource(idx); err != nil {
			return err
		}
	}
	if meta.Sensitive() {
		if err := s.backend.set(key, value); err != nil {
			return err
		}
		if s.keySource != "" {
			idx.data.KeySource = string(s.keySource)
		}
		return nil
	}
	if wasSensitive {
		return s.backend.remove(key)
	}
	return nil
}

// checkKeySource rejects a key-source change with a clear error instead of a
// downstream decryption failure.
func (s *localStore) checkKeySource(idx *index) error {
	if s.keySource == "" || idx.data.KeySource == "" {
		return nil
	}
	if idx.data.KeySource != string(s.keySource) {
		return fmt.Errorf(
			"secrets were encrypted with the %q key source but the current source is %q; "+
				"restore the original DEVSY_SECRETS_PASSPHRASE (or unset it) or re-create the secrets",
			idx.data.KeySource, s.keySource,
		)
	}

	return nil
}
