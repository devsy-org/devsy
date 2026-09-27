package provider

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// GetProviderOperationLock returns a file-based lock that serializes
// broad lifecycle operations (add, set-source, update, set, init, delete, rename)
// for the given provider within the given context.
func GetProviderOperationLock(contextName, providerName string) (*flock.Flock, error) {
	locksDir, err := GetLocksDir(contextName)
	if err != nil {
		return nil, fmt.Errorf("get locks dir: %w", err)
	}

	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}

	return flock.New(
		filepath.Join(locksDir, providerName+".provider-operation.lock"),
	), nil
}
