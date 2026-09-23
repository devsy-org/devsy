package machinediagnostics

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultRuntimeDir      = "/run/devsy"
	RuntimeLockFileName    = "agent-daemon.lock"
	RuntimeLocatorFileName = "agent-daemon.json"
)

// RuntimePaths keeps the daemon's singleton lock and discovery metadata in the
// same runtime directory.
type RuntimePaths struct {
	LockPath    string
	LocatorPath string
}

func SystemRuntimePaths() RuntimePaths {
	return runtimePaths(DefaultRuntimeDir)
}

func UserRuntimePaths(userCacheDir func() (string, error)) (RuntimePaths, error) {
	cacheDir, err := userCacheDir()
	if err != nil {
		return RuntimePaths{}, fmt.Errorf("get user cache directory for daemon runtime: %w", err)
	}
	return runtimePaths(filepath.Join(cacheDir, "devsy")), nil
}

func runtimePaths(dir string) RuntimePaths {
	return RuntimePaths{
		LockPath:    filepath.Join(dir, RuntimeLockFileName),
		LocatorPath: filepath.Join(dir, RuntimeLocatorFileName),
	}
}

// EnsureRuntimeDir creates a runtime directory, repairing permissions on
// shared system directories that predate them.
func EnsureRuntimeDir(path string, shared bool) error {
	mode := os.FileMode(0o750)
	if shared {
		mode = 0o755
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if shared {
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func ensureRuntimeDirForPath(path string) error {
	dir := filepath.Dir(path)
	return EnsureRuntimeDir(dir, filepath.Clean(dir) == DefaultRuntimeDir)
}
