package secrets

import (
	"errors"
	"fmt"

	keyring "github.com/zalando/go-keyring"
)

const keyringService = "sh.devsy.secrets"

type keyringBackend struct{}

func (keyringBackend) set(key, value string) error {
	if err := keyring.Set(keyringService, key, value); err != nil {
		return fmt.Errorf("write to keyring: %w", err)
	}

	return nil
}

func (keyringBackend) get(key string) (string, error) {
	value, err := keyring.Get(keyringService, key)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrSecretNotFound
		}

		return "", fmt.Errorf("read from keyring: %w", err)
	}

	return value, nil
}

func (keyringBackend) remove(key string) error {
	if err := keyring.Delete(keyringService, key); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}

		return fmt.Errorf("delete from keyring: %w", err)
	}

	return nil
}

// keyringAvailable probes with a sentinel round-trip, since the keyring can be
// present but unusable (e.g. no D-Bus session on headless Linux).
func keyringAvailable() bool {
	const probeKey = "__devsy_probe__"
	if err := keyring.Set(keyringService, probeKey, "1"); err != nil {
		return false
	}
	_ = keyring.Delete(keyringService, probeKey)

	return true
}
