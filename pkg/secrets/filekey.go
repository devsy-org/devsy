package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	keyring "github.com/zalando/go-keyring"
)

const KeyFileName = "secrets.key"

const keyringKeyUser = "__filekey__"

type keySource string

const (
	keySourcePassphrase keySource = "passphrase"
	keySourceKeyring    keySource = "keyring"
	keySourceAutoFile   keySource = "file"
)

type fileKey struct {
	recipient age.Recipient
	identity  age.Identity
	source    keySource
}

// resolveFileKey resolves the age key by precedence: passphrase, keyring, file.
func resolveFileKey(dir, passphrase string) (*fileKey, error) {
	if passphrase != "" {
		recipient, err := age.NewScryptRecipient(passphrase)
		if err != nil {
			return nil, fmt.Errorf("derive secrets key: %w", err)
		}
		identity, err := age.NewScryptIdentity(passphrase)
		if err != nil {
			return nil, fmt.Errorf("derive secrets key: %w", err)
		}

		return &fileKey{recipient: recipient, identity: identity, source: keySourcePassphrase}, nil
	}

	if keyringAvailable() {
		return resolveAutoKey(dir, keyringKeyStore{}, keySourceKeyring)
	}

	return resolveAutoKey(
		dir,
		fileKeyStore{path: filepath.Join(dir, KeyFileName)},
		keySourceAutoFile,
	)
}

// resolveAutoKey reuses the persisted identity or creates one; it must never
// regenerate an existing key or previously encrypted secrets become unreadable.
// The read-generate-save-reload sequence is locked so concurrent first-time
// initializers converge on one persisted key.
func resolveAutoKey(dir string, store keyStore, source keySource) (*fileKey, error) {
	unlock, err := acquireFlock(dir, KeyFileName+".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()

	encoded, err := store.load()
	if err != nil {
		return nil, err
	}

	if encoded == "" {
		identity, gErr := age.GenerateX25519Identity()
		if gErr != nil {
			return nil, fmt.Errorf("generate secrets key: %w", gErr)
		}
		if err := store.save(identity.String()); err != nil {
			return nil, err
		}
		if encoded, err = store.load(); err != nil {
			return nil, err
		}
	}

	identity, err := age.ParseX25519Identity(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("parse stored secrets key: %w", err)
	}

	return &fileKey{recipient: identity.Recipient(), identity: identity, source: source}, nil
}

type keyStore interface {
	load() (string, error) // returns "" when no key is stored yet
	save(value string) error
}

type keyringKeyStore struct{}

func (keyringKeyStore) load() (string, error) {
	value, err := keyring.Get(keyringService, keyringKeyUser)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", nil
		}

		return "", fmt.Errorf("read secrets key from keyring: %w", err)
	}

	return value, nil
}

func (keyringKeyStore) save(value string) error {
	if err := keyring.Set(keyringService, keyringKeyUser, value); err != nil {
		return fmt.Errorf("write secrets key to keyring: %w", err)
	}

	return nil
}

type fileKeyStore struct {
	path string
}

func (s fileKeyStore) load() (string, error) {
	data, err := os.ReadFile(s.path) // #nosec G304 -- path derived from config dir.
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}

		return "", fmt.Errorf("read secrets key file: %w", err)
	}

	return string(data), nil
}

// save writes the key with O_EXCL so a losing concurrent initializer leaves the
// winner's key intact.
func (s fileKeyStore) save(value string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	f, err := os.OpenFile(
		s.path,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	) // #nosec G304 -- path derived from config dir.
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("write secrets key file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(value); err != nil {
		return fmt.Errorf("write secrets key file: %w", err)
	}

	return nil
}
