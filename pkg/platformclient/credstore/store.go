// Package credstore keeps the gateway session token in the OS keychain. Where
// no keychain works it falls back to a file with mode 0600.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Service is the keychain service name. It differs from the one used by the
// managed secret store, so the two never share entries.
const Service = "sh.devsy.gateway"

const (
	account  = "session"
	fileName = "gateway-credentials.json"
)

// ErrNotLoggedIn means no credential is stored.
var ErrNotLoggedIn = errors.New("not logged in")

// maxKeychainBytes keeps the value under the smallest limit of the platform
// keychains (Windows allows 2560 bytes). A larger value goes to the file.
const maxKeychainBytes = 2400

// ErrUnconfirmed means Delete could not tell whether the OS keychain is clear.
// Callers can print it as a warning, because the local file is already gone.
var ErrUnconfirmed = errors.New("could not confirm removal from the OS keychain")

// ErrKeychainUnreadable means no login was found and the OS keychain could not
// be read, so a login may still be stored there. Unlock the keychain or log in
// again.
var ErrKeychainUnreadable = errors.New(
	"could not read the OS keychain; unlock it or log in again",
)

var (
	errUnreadable  = errors.New("stored gateway credential is unreadable")
	errStale       = errors.New("could not replace the session token held in the OS keychain")
	errStillStored = errors.New("could not remove the session token from the OS keychain")
)

// Credential is the login for one gateway.
type Credential struct {
	GatewayURL string    `json:"gatewayUrl"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expiresAt"`
	UserID     string    `json:"userId,omitempty"`
}

// String hides the token, so a credential printed by mistake stays safe.
func (c Credential) String() string {
	return fmt.Sprintf(
		"Credential{GatewayURL:%s UserID:%s ExpiresAt:%s Token:[redacted]}",
		c.GatewayURL, c.UserID, c.ExpiresAt.Format(time.RFC3339),
	)
}

// GoString matches String for the %#v verb.
func (c Credential) GoString() string { return c.String() }

// LogValue hides the token in structured logs.
func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("gatewayUrl", c.GatewayURL),
		slog.String("userId", c.UserID),
		slog.Time("expiresAt", c.ExpiresAt),
	)
}

// Expired reports whether the session has run out at now.
func (c Credential) Expired(now time.Time) bool { return !now.Before(c.ExpiresAt) }

func (c Credential) validate() error {
	if c.GatewayURL == "" || c.Token == "" || c.ExpiresAt.IsZero() {
		return errors.New("incomplete gateway credential")
	}
	return nil
}

// Store saves and loads the credential. Only one login is kept, because the
// gateway session is a single account.
type Store struct {
	keychain keychain
	dir      func() (string, error)
}

// New returns a store on the OS keychain. The fallback file sits next to the
// Devsy config file.
func New() *Store {
	return &Store{keychain: systemKeychain{}, dir: configDir}
}

// Save stores the credential in the keychain. When the keychain does not work
// it writes a file with mode 0600 and returns a one-line warning that names it.
// The warning never contains the token.
func (s *Store) Save(cred Credential) (warning string, err error) {
	if err := cred.validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return "", errors.New("encode gateway credential")
	}
	if len(data) <= maxKeychainBytes && s.keychain.Set(string(data)) == nil {
		return s.removeOldFile(), nil
	}
	return s.saveToFile(data)
}

// Load returns the stored credential, or ErrNotLoggedIn. It does not check the
// expiry. When both the keychain and the file hold a login, the later expiry
// wins, so a keychain entry that could not be removed while the keychain was
// locked never hides a newer login. When the keychain cannot be read, Load uses
// the file if there is one. With no file it returns ErrKeychainUnreadable, not
// ErrNotLoggedIn, because a login may be stored where Load cannot see it.
func (s *Store) Load() (Credential, error) {
	value, err := s.keychain.Get()
	if err != nil {
		return s.loadWithoutKeychain(err)
	}
	cred, err := decode([]byte(value))
	if err != nil {
		return Credential{}, err
	}
	fromFile, fileErr := s.loadFile()
	if fileErr == nil && fromFile.ExpiresAt.After(cred.ExpiresAt) {
		return fromFile, nil
	}
	return cred, nil
}

// Delete removes the credential from the keychain and the file. It always
// removes the file. It returns ErrUnconfirmed when it cannot tell that the
// keychain is clear, even if a file existed, because an entry saved before the
// keychain locked may remain. Once the keychain is readable again, Load returns
// that entry, so the caller should show the error as a warning and offer a
// retry.
func (s *Store) Delete() error {
	return errors.Join(s.clearKeychain(), s.removeFile())
}

// removeOldFile drops a fallback file that the keychain write replaced. A
// failure is only a warning, because the keychain already holds the login.
func (s *Store) removeOldFile() string {
	if s.removeFile() == nil {
		return ""
	}
	path, _ := s.filePath()
	return fmt.Sprintf("could not remove the old credential file %s; delete it by hand", path)
}

func (s *Store) loadWithoutKeychain(keychainErr error) (Credential, error) {
	cred, err := s.loadFile()
	if errors.Is(err, ErrNotLoggedIn) && !errors.Is(keychainErr, errKeychainNotFound) {
		return Credential{}, ErrKeychainUnreadable
	}
	return cred, err
}

func (s *Store) clearKeychain() error {
	if s.keychain.Delete() == nil {
		return nil
	}
	_, err := s.keychain.Get()
	switch {
	case errors.Is(err, errKeychainNotFound):
		return nil
	case err == nil:
		return errStillStored
	}
	return ErrUnconfirmed
}

func decode(data []byte) (Credential, error) {
	var cred Credential
	if err := json.Unmarshal(data, &cred); err != nil || cred.validate() != nil {
		return Credential{}, errUnreadable
	}
	return cred, nil
}

func (s *Store) saveToFile(data []byte) (string, error) {
	path, err := s.writeFile(data)
	if err != nil {
		return "", err
	}
	// Load prefers the keychain, so an older entry there would shadow the new file.
	if err := s.dropKeychainEntry(); err != nil {
		_ = s.removeFile()
		return "", err
	}
	return fmt.Sprintf(
		"OS keychain not used; the session token is stored in %s (mode 0600)", path,
	), nil
}

func (s *Store) dropKeychainEntry() error {
	if s.keychain.Delete() == nil {
		return nil
	}
	if _, err := s.keychain.Get(); err == nil {
		return errStale
	}
	return nil
}
