package secrets

import "errors"

var (
	ErrUnlockRequired     = errors.New("secret store unlock required")
	ErrUnlockFailed       = errors.New("secret store unlock failed")
	ErrBackendUnavailable = errors.New("secret backend unavailable")
	ErrStoreCorrupt       = errors.New("secret store corrupt")
)

type UnlockRequiredError struct{ Backend Backend }

func (e *UnlockRequiredError) Error() string {
	return "secret store unlock required; supply a passphrase or configure a credential source"
}
func (e *UnlockRequiredError) Unwrap() error { return ErrUnlockRequired }

type UnlockFailedError struct {
	Backend Backend
	Cause   error
}

func (e *UnlockFailedError) Error() string {
	return "secret store unlock failed; verify the credential and encrypted store"
}
func (e *UnlockFailedError) Unwrap() []error { return []error{ErrUnlockFailed, e.Cause} }

type BackendUnavailableError struct {
	Backend Backend
	Cause   error
}

func (e *BackendUnavailableError) Error() string {
	return "secret backend unavailable; restore access to the credential service or key"
}

func (e *BackendUnavailableError) Unwrap() []error { return []error{ErrBackendUnavailable, e.Cause} }

type StoreCorruptError struct{ Cause error }

func (e *StoreCorruptError) Error() string {
	return "secret store corrupt; restore a valid store backup"
}
func (e *StoreCorruptError) Unwrap() []error { return []error{ErrStoreCorrupt, e.Cause} }
