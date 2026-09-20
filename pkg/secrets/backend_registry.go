package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

type backendRegistry interface {
	Open(kind Backend, idx *index, create bool) (backend, error)
	ResolveForNewSecret(preference Backend, idx *index) (Backend, error)
	Probe(kind Backend, idx *index, key string) (present, conclusive bool)
}

type fixedBackendRegistry struct{ b backend }

func (r fixedBackendRegistry) Open(_ Backend, _ *index, _ bool) (backend, error) { return r.b, nil }
func (r fixedBackendRegistry) ResolveForNewSecret(_ Backend, _ *index) (Backend, error) {
	return BackendKeyring, nil
}
func (r fixedBackendRegistry) Probe(kind Backend, _ *index, key string) (bool, bool) {
	if kind != BackendKeyring {
		return false, false
	}
	return probePresence(r.b, key)
}

type systemBackendRegistry struct{ dir string }

func newSystemBackendRegistry(
	dir string,
) backendRegistry {
	return &systemBackendRegistry{dir: dir}
}

func (r *systemBackendRegistry) Open(kind Backend, idx *index, create bool) (backend, error) {
	switch kind {
	case BackendKeyring:
		return keyringBackend{}, nil
	case BackendFile:
		var key *fileKey
		var err error
		if create {
			key, err = resolveFileKey(r.dir, os.Getenv(EnvPassphrase))
		} else {
			key, err = openExistingFileKey(r.dir, idx)
		}
		if err != nil {
			return nil, err
		}
		if create && idx != nil {
			idx.data.KeySource = string(key.source)
		}
		return newFileBackend(filepath.Join(r.dir, EncryptedFileName), key), nil
	default:
		return nil, fmt.Errorf("invalid secrets backend %q", kind)
	}
}

func (r *systemBackendRegistry) Probe(kind Backend, idx *index, key string) (bool, bool) {
	switch kind {
	case BackendKeyring:
		if !keyringAvailable() {
			return false, false
		}
		return probePresence(keyringBackend{}, key)
	case BackendFile:
		path := filepath.Join(r.dir, EncryptedFileName)
		if _, err := os.Stat(path); err != nil {
			return false, true
		}
		fk, err := openExistingFileKey(r.dir, idx)
		if err != nil && idx.data.KeySource == "" {
			fk, err = openPassphraseFileKey()
		}
		if err != nil {
			return false, false
		}
		return probePresence(newFileBackend(path, fk), key)
	default:
		return false, false
	}
}

func probePresence(b backend, key string) (present, conclusive bool) {
	_, err := b.get(key)
	switch {
	case err == nil:
		return true, true
	case errors.Is(err, ErrSecretNotFound):
		return false, true
	default:
		return false, false
	}
}

func (r *systemBackendRegistry) ResolveForNewSecret(
	preference Backend,
	idx *index,
) (Backend, error) {
	switch preference {
	case BackendKeyring:
		return BackendKeyring, nil
	case BackendFile:
		return BackendFile, nil
	case BackendAuto:
		if keyringAvailable() {
			return BackendKeyring, nil
		}
		return BackendFile, nil
	default:
		return "", fmt.Errorf("invalid secrets backend %q", preference)
	}
}

func openExistingFileKey(dir string, idx *index) (*fileKey, error) {
	source := keySource(idx.data.KeySource)
	if source == "" {
		return nil, fmt.Errorf("encrypted secrets are missing their key source metadata")
	}
	if source == keySourcePassphrase {
		return openPassphraseFileKey()
	}
	var store keyStore
	switch source {
	case keySourceKeyring:
		store = keyringKeyStore{}
	case keySourceAutoFile:
		store = fileKeyStore{path: filepath.Join(dir, KeyFileName)}
	default:
		return nil, fmt.Errorf("invalid secrets key source %q", source)
	}
	return keyFromStore(store, source)
}

func openPassphraseFileKey() (*fileKey, error) {
	passphrase := os.Getenv(EnvPassphrase)
	if passphrase == "" {
		return nil, fmt.Errorf("encrypted secrets require %s", EnvPassphrase)
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	return &fileKey{recipient: recipient, identity: identity, source: keySourcePassphrase}, nil
}

func keyFromStore(store keyStore, source keySource) (*fileKey, error) {
	encoded, err := store.load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(encoded) == "" {
		return nil, ErrSecretNotFound
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("parse stored secrets key: %w", err)
	}
	return &fileKey{recipient: identity.Recipient(), identity: identity, source: source}, nil
}
