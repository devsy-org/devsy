package provider

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// GetProviderOperationLock returns a file-based lock that serializes
// broad lifecycle operations (add, set-source, update, set, init, delete, rename)
// for the given provider within the given context.
func GetProviderOperationLock(contextName, providerName string) (*flock.Flock, error) {
	if providerName == "" || ProviderNameRegEx.MatchString(providerName) || len(providerName) > 32 {
		return nil, fmt.Errorf("invalid provider name %q", providerName)
	}
	locksDir, err := ensureOperationLocksDir(contextName)
	if err != nil {
		return nil, err
	}
	return flock.New(filepath.Join(locksDir, providerName+".provider-operation.lock")), nil
}

// LockProvider acquires the operation lock for providerName in contextName and
// returns the func that releases it. Errors are wrapped as "get operation lock"
// or "acquire operation lock". Callers defer the returned func.
func LockProvider(contextName, providerName string) (func(), error) {
	return lockProvider(contextName, providerName, "")
}

// LockProviderNamed is LockProvider with the provider name in the error text
// ("get operation lock for <name>: ...").
func LockProviderNamed(contextName, providerName string) (func(), error) {
	return lockProvider(contextName, providerName, " for "+providerName)
}

func lockProvider(contextName, providerName, errSuffix string) (func(), error) {
	opLock, err := GetProviderOperationLock(contextName, providerName)
	if err != nil {
		return nil, fmt.Errorf("get operation lock%s: %w", errSuffix, err)
	}
	if err := opLock.Lock(); err != nil {
		return nil, fmt.Errorf("acquire operation lock%s: %w", errSuffix, err)
	}
	return func() { _ = opLock.Unlock() }, nil
}

// GetPlatformInstanceOperationLock serializes creation of one Platform host without
// sharing a lock file with provider rename or lifecycle operations.
func GetPlatformInstanceOperationLock(contextName, host string) (*flock.Flock, error) {
	if host == "" {
		return nil, fmt.Errorf("platform instance host is empty")
	}
	locksDir, err := ensureOperationLocksDir(contextName)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(ToPlatformInstanceID(host)))
	return flock.New(filepath.Join(locksDir, fmt.Sprintf("pro-instance-%x.lock", digest[:16]))), nil
}

func ensureOperationLocksDir(contextName string) (string, error) {
	locksDir, err := GetLocksDir(contextName)
	if err != nil {
		return "", fmt.Errorf("get locks dir: %w", err)
	}
	if err := os.MkdirAll(locksDir, 0o700); err != nil {
		return "", fmt.Errorf("create locks dir: %w", err)
	}
	return locksDir, nil
}
