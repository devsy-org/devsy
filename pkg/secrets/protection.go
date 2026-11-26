package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	keyring "github.com/zalando/go-keyring"
)

const rememberedPassphraseUser = keyringPassphraseUser

func ReadRememberedPassphrase() (string, error) {
	value, err := keyring.Get(keyringService, rememberedPassphraseUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", ErrBackendUnavailable
	}
	return value, nil
}

func forgetRememberedPassphrase() error {
	err := keyring.Delete(keyringService, rememberedPassphraseUser)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return ErrBackendUnavailable
	}
	return nil
}

type ProtectionStatus struct {
	Availability        SecretAvailability `json:"availability"`
	ReasonCode          string             `json:"reasonCode,omitempty"`
	KeySource           string             `json:"keySource"`
	Remembered          bool               `json:"remembered"`
	RememberedAvailable bool               `json:"rememberedAvailable"`
	FileEntries         []SecretMeta       `json:"fileEntries"`
}

type ProtectionManager struct {
	dir              string
	resolver         UnlockMaterialResolver
	readRemembered   func() (string, error)
	saveRemembered   func(string) error
	forgetRemembered func() error
	// resolveNewFileKey overrides only the rekey target for individual test managers.
	resolveNewFileKey func(dir, passphrase string) (*fileKey, error)
	// phase is used for deterministic crash injection in recovery tests.
	phase func(string) error
}

func NewProtectionManager(dir string, resolver UnlockMaterialResolver) *ProtectionManager {
	if resolver == nil {
		resolver = DefaultUnlockResolver{}
	}
	return &ProtectionManager{
		dir: dir, resolver: resolver, readRemembered: ReadRememberedPassphrase,
		saveRemembered: func(value string) error {
			if keyring.Set(keyringService, rememberedPassphraseUser, value) != nil {
				return fmt.Errorf(
					"remember requires an OS credential service; supply unlock input through "+
						"environment or a passphrase file instead: %w",
					ErrBackendUnavailable,
				)
			}
			return nil
		},
		forgetRemembered: forgetRememberedPassphrase,
	}
}

func (p *ProtectionManager) Status() (ProtectionStatus, error) { return p.status(true) }

// CatalogStatus reads the confirmation list without inspecting value backends.
func (p *ProtectionManager) CatalogStatus() (ProtectionStatus, error) { return p.status(false) }

func fileEntries(idx *index) []SecretMeta {
	entries := []SecretMeta{}
	for ctx, names := range idx.data.Contexts {
		for name, meta := range names {
			if meta.Backend == BackendFile {
				meta.Context = ctx
				meta.Name = name
				meta.Value = ""
				entries = append(entries, meta)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Context != entries[j].Context {
			return entries[i].Context < entries[j].Context
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}

func (p *ProtectionManager) Remember(passphrase string) error {
	if passphrase == "" {
		return errors.New("passphrase must not be empty")
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	idx, err := loadIndex(filepath.Join(p.dir, IndexFileName))
	if err != nil {
		return err
	}
	if idx.data.KeySource != string(keySourcePassphrase) {
		return errors.New("file store is not passphrase protected")
	}
	if err = p.verifyPassphrase(passphrase); err != nil {
		return err
	}
	return p.saveRemembered(passphrase)
}

func (p *ProtectionManager) Forget() error {
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return p.forgetRemembered()
}

func (p *ProtectionManager) SetPassphrase(passphrase string) error {
	return p.rekey(passphrase, false, false)
}

func (p *ProtectionManager) ChangePassphrase(passphrase string) error {
	return p.rekey(passphrase, true, false)
}
func (p *ProtectionManager) RemovePassphrase() error { return p.rekey("", true, true) }

// ResetFileStore quarantines the whole ciphertext and removes every file-owned
// catalog entry. The command layer must obtain destructive confirmation first.
func (p *ProtectionManager) ResetFileStore() (string, error) {
	return p.resetFileStore(nil)
}

// ResetFileStoreIfUnchanged prevents deleting entries created after the user
// reviewed the destructive confirmation list.
func (p *ProtectionManager) ResetFileStoreIfUnchanged(expected []SecretMeta) (string, error) {
	return p.resetFileStore(expected)
}

func sameFileEntries(current, expected []SecretMeta) bool {
	if len(current) != len(expected) {
		return false
	}
	for i, meta := range current {
		if meta.Context != expected[i].Context || meta.Name != expected[i].Name {
			return false
		}
	}
	return true
}

// ResolvePassphrase resolves current unlock input for remember without putting
// it in argv. New protection credentials are read separately by the command.
func (p *ProtectionManager) ResolvePassphrase(ctx context.Context) (string, error) {
	material, err := p.resolver.ResolvePassphrase(
		ctx,
		UnlockRequest{AllowPrompt: true, Purpose: "remember"},
	)
	return material.Passphrase, err
}

func (p *ProtectionManager) lock() (func(), error) {
	unlock, err := acquireFlock(p.dir, IndexFileName+".lock")
	if err != nil {
		return nil, err
	}
	if err = RecoverRekey(p.dir); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

func (p *ProtectionManager) status(inspect bool) (ProtectionStatus, error) {
	unlock, err := p.lock()
	if err != nil {
		return ProtectionStatus{}, err
	}
	defer unlock()
	idx, err := loadIndex(filepath.Join(p.dir, IndexFileName))
	if err != nil {
		return ProtectionStatus{}, err
	}
	result := ProtectionStatus{
		KeySource:    idx.data.KeySource,
		FileEntries:  fileEntries(idx),
		Availability: SecretStateUnknown,
	}
	// Credential readiness is independent of legacy file ownership and I/O.
	// Metadata-only confirmation lists never inspect the credential service.
	if inspect {
		value, rememberErr := p.readRemembered()
		result.Remembered = value != ""
		result.RememberedAvailable = rememberErr == nil
	}
	// Keep protection metadata visible when a legacy owner cannot be proven.
	// Reset applies the same ownership check as a hard failure under its lock.
	ownershipErr := p.ensureKnownFileOwnership(idx)
	if errors.Is(ownershipErr, ErrStateIndeterminate) {
		result.ReasonCode = "ownership_unknown"
		return result, nil
	}
	if ownershipErr != nil {
		result.Availability, result.ReasonCode = inspectionAvailability(ownershipErr)
		return result, nil
	}
	if inspect {
		result.Availability, result.ReasonCode = p.fileAvailability(idx)
	}
	return result, nil
}

func (p *ProtectionManager) fileAvailability(idx *index) (SecretAvailability, string) {
	if idx.data.KeySource == "" {
		return SecretStateUnknown, "file_store_uninitialized"
	}
	if _, err := os.Stat(filepath.Join(p.dir, EncryptedFileName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SecretMissing, "file_store_missing"
		}
		return SecretBackendUnavailable, "backend_unavailable"
	}
	noPrompt := UnlockMaterialResolverFunc(
		func(ctx context.Context, request UnlockRequest) (UnlockMaterial, error) {
			request.AllowPrompt = false
			return p.resolver.ResolvePassphrase(ctx, request)
		},
	)
	key, err := openExistingFileKeyWithResolver(p.dir, idx, noPrompt)
	if err == nil {
		_, err = newFileBackend(filepath.Join(p.dir, EncryptedFileName), key).load()
	}
	return inspectionAvailability(err)
}

func (p *ProtectionManager) checkpoint(phase string) error {
	if p.phase != nil {
		return p.phase(phase)
	}
	return nil
}

func (p *ProtectionManager) verifyPassphrase(passphrase string) error {
	if _, err := os.Stat(filepath.Join(p.dir, EncryptedFileName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSecretNotFound
		}
		return err
	}
	key, err := passphraseFileKey(passphrase)
	if err != nil {
		return err
	}
	_, err = newFileBackend(filepath.Join(p.dir, EncryptedFileName), key).load()
	return err
}

func (p *ProtectionManager) forgetExistingRemembered() error {
	remembered, err := p.readRemembered()
	if err != nil || remembered == "" {
		return nil
	}
	return p.forgetRemembered()
}
