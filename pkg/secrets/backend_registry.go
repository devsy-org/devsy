package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

type BackendOpenIntent string

const (
	BackendOpenExisting  BackendOpenIntent = "open_existing"
	BackendInspect       BackendOpenIntent = "inspect"
	BackendInitializeNew BackendOpenIntent = "initialize_new"
)

type backendRegistry interface {
	Open(kind Backend, idx *index, intent BackendOpenIntent) (backend, error)
	ResolveForNewSecret(preference Backend, idx *index) (Backend, error)
	Probe(kind Backend, idx *index, key string) (present, conclusive bool)
}

type fixedBackendRegistry struct{ b backend }

func (r fixedBackendRegistry) Open(_ Backend, _ *index, _ BackendOpenIntent) (backend, error) {
	return r.b, nil
}

func (r fixedBackendRegistry) ResolveForNewSecret(_ Backend, _ *index) (Backend, error) {
	return BackendKeyring, nil
}

func (r fixedBackendRegistry) Probe(kind Backend, _ *index, key string) (bool, bool) {
	if kind != BackendKeyring {
		return false, false
	}
	return probePresence(r.b, key)
}

type systemBackendRegistry struct {
	dir         string
	resolver    UnlockMaterialResolver
	allowPrompt bool
}

func newSystemBackendRegistry(
	dir string, resolvers ...UnlockMaterialResolver,
) backendRegistry {
	resolver := UnlockMaterialResolver(DefaultUnlockResolver{})
	if len(resolvers) > 0 && resolvers[0] != nil {
		resolver = resolvers[0]
	}
	return &systemBackendRegistry{dir: dir, resolver: resolver}
}

func (r *systemBackendRegistry) Open(
	kind Backend,
	idx *index,
	intent BackendOpenIntent,
) (backend, error) {
	switch kind {
	case BackendKeyring:
		return keyringBackend{}, nil
	case BackendFile:
		return r.openFileBackend(idx, intent)
	default:
		return nil, fmt.Errorf("invalid secrets backend %q", kind)
	}
}

func (r *systemBackendRegistry) Probe(kind Backend, idx *index, key string) (bool, bool) {
	switch kind {
	case BackendKeyring:
		return probeKeyring(key)
	case BackendFile:
		return r.probeFile(idx, key)
	default:
		return false, false
	}
}

func probeKeyring(key string) (bool, bool) {
	if !keyringAvailable() {
		return false, false
	}
	return probePresence(keyringBackend{}, key)
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

func (r *systemBackendRegistry) openFileBackend(
	idx *index,
	intent BackendOpenIntent,
) (backend, error) {
	key, err := r.openFileKey(idx, intent)
	if err != nil {
		return nil, err
	}
	if intent == BackendInitializeNew && idx != nil {
		idx.data.KeySource = string(key.source)
	}
	return newFileBackend(filepath.Join(r.dir, EncryptedFileName), key), nil
}

func (r *systemBackendRegistry) initializeFileKey() (*fileKey, error) {
	if _, statErr := os.Stat(filepath.Join(r.dir, EncryptedFileName)); statErr == nil {
		return nil, &StoreCorruptError{}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	material, resolveErr := r.resolver.ResolvePassphrase(
		context.Background(),
		UnlockRequest{Purpose: "initialize"},
	)
	if resolveErr != nil && !errors.Is(resolveErr, ErrUnlockRequired) {
		return nil, resolveErr
	}
	return resolveFileKey(r.dir, material.Passphrase)
}

func (r *systemBackendRegistry) probeFile(idx *index, key string) (bool, bool) {
	path := filepath.Join(r.dir, EncryptedFileName)
	if _, err := os.Stat(path); err != nil {
		return false, errors.Is(err, os.ErrNotExist)
	}
	fk, err := openExistingFileKeyWithResolver(r.dir, idx, r.resolver)
	if err != nil && idx.data.KeySource == "" {
		fk, err = openPassphraseFileKeyWithResolver(r.resolver)
	}
	if err != nil {
		return false, false
	}
	return probePresence(newFileBackend(path, fk), key)
}

func openExistingFileKeyWithResolver(
	dir string,
	idx *index,
	resolver UnlockMaterialResolver,
) (*fileKey, error) {
	source := keySource(idx.data.KeySource)
	if source == "" {
		return nil, &StoreCorruptError{}
	}
	if source == keySourcePassphrase {
		return openPassphraseFileKeyWithResolver(resolver)
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
	key, err := keyFromStore(store, source)
	if err != nil {
		return nil, &BackendUnavailableError{Backend: BackendFile, Cause: err}
	}
	return key, nil
}

func openPassphraseFileKey() (*fileKey, error) {
	return openPassphraseFileKeyWithResolver(DefaultUnlockResolver{})
}

func openPassphraseFileKeyWithResolver(resolver UnlockMaterialResolver) (*fileKey, error) {
	material, err := resolver.ResolvePassphrase(
		context.Background(),
		UnlockRequest{AllowPrompt: true, Purpose: "open"},
	)
	if err != nil {
		return nil, err
	}
	return passphraseFileKey(material.Passphrase)
}

func passphraseFileKey(passphrase string) (*fileKey, error) {
	if passphrase == "" {
		return nil, &UnlockRequiredError{Backend: BackendFile}
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

type promptPolicyResolver struct {
	resolver UnlockMaterialResolver
	allow    bool
}

func (r promptPolicyResolver) ResolvePassphrase(
	ctx context.Context,
	request UnlockRequest,
) (UnlockMaterial, error) {
	request.AllowPrompt = request.AllowPrompt && r.allow
	return r.resolver.ResolvePassphrase(ctx, request)
}

func (r *systemBackendRegistry) openFileKey(
	idx *index,
	intent BackendOpenIntent,
) (*fileKey, error) {
	resolver := promptPolicyResolver{
		resolver: r.resolver,
		allow:    r.allowPrompt && intent != BackendInspect,
	}
	// Persisted protection always wins over invocation credentials.
	switch {
	case idx != nil && idx.data.KeySource != "":
		return openExistingFileKeyWithResolver(
			r.dir,
			idx,
			resolver,
		)
	case intent == BackendInitializeNew:
		return r.initializeFileKey()
	default:
		return openExistingFileKeyWithResolver(
			r.dir,
			idx,
			resolver,
		)
	}
}
