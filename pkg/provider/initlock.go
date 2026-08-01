package provider

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/gofrs/flock"
)

// InitState is the lifecycle state of a provider's initialization.
type InitState string

const (
	InitStateNotInitialized InitState = "not_initialized"
	InitStateInitializing   InitState = "initializing"
	InitStateInitialized    InitState = "initialized"
	InitStateFailed         InitState = "failed"
)

// GetProviderInitLock returns the advisory lock used to signal that a
// provider's init command is currently running. Held for the duration of
// Exec.Init in initProvider; ResolveInitState probes it (without blocking) to
// tell a live "initializing" run apart from one abandoned by a crashed
// process, since the OS releases the lock the moment that process dies.
func GetProviderInitLock(contextName, name string) (*flock.Flock, error) {
	locksDir, err := GetLocksDir(contextName)
	if err != nil {
		return nil, fmt.Errorf("get locks dir: %w", err)
	}
	// #nosec G301 -- mirrors existing lock dir permissions elsewhere in this package
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}

	return flock.New(filepath.Join(locksDir, name+".provider-init.lock")), nil
}

// ResolveInitState derives the current init lifecycle state for a provider
// from its persisted config and the live state of its init lock.
func ResolveInitState(contextName, name string, state *config.ProviderConfig) (InitState, error) {
	if state != nil && state.Initialized {
		return InitStateInitialized, nil
	}

	lock, err := GetProviderInitLock(contextName, name)
	if err != nil {
		return "", err
	}

	locked, err := lock.TryLock()
	if err != nil {
		return "", fmt.Errorf("check init lock: %w", err)
	}
	if !locked {
		return InitStateInitializing, nil
	}
	defer func() { _ = lock.Unlock() }()

	if state != nil && state.InitAttempted {
		// The lock is free but a prior attempt never reached Initialized:
		// it either failed cleanly or was abandoned by a crashed process.
		return InitStateFailed, nil
	}

	return InitStateNotInitialized, nil
}
