package credstore

import (
	"errors"

	keyring "github.com/zalando/go-keyring"
)

var errKeychainNotFound = errors.New("keychain entry not found")

type keychain interface {
	Set(value string) error
	Get() (string, error)
	Delete() error
}

type systemKeychain struct{}

func (systemKeychain) Set(value string) error {
	return keyring.Set(Service, account, value)
}

func (systemKeychain) Get() (string, error) {
	value, err := keyring.Get(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errKeychainNotFound
	}
	return value, err
}

func (systemKeychain) Delete() error {
	err := keyring.Delete(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
