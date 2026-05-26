//go:build !windows

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const agentLockFilename = ".owner.lock"

// agentLockFDs keeps the lockfile descriptors for each owned socket dir
// alive for the lifetime of the process. The kernel auto-releases the
// flock when the process dies for any reason (including SIGKILL), which is
// what lets a later helper invocation's startup janitor detect orphaned
// directories.
var (
	agentLockFDsMu sync.Mutex
	agentLockFDs   []*os.File
)

// takeAgentDirLock opens the lockfile inside dir and acquires an exclusive
// non-blocking flock on it, retaining the descriptor in a process-wide slot
// so the lock is held until process exit.
func takeAgentDirLock(dir string) error {
	lockPath := filepath.Join(dir, agentLockFilename)
	// #nosec G304 -- lockPath is derived from controlled inputs (runtimeDir + connID).
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lockfile: %w", err)
	}
	fd := int(f.Fd()) //nolint:gosec // os.File.Fd() fits in int on supported platforms
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return fmt.Errorf("flock lockfile %s: %w", lockPath, err)
	}
	agentLockFDsMu.Lock()
	agentLockFDs = append(agentLockFDs, f)
	agentLockFDsMu.Unlock()
	return nil
}

// releaseAgentDirLock drops a previously-acquired lock on dir's lockfile.
// Used on setup failure paths after takeAgentDirLock succeeded; the steady-
// state lifetime is process exit, where the kernel auto-releases.
func releaseAgentDirLock(dir string) {
	lockPath := filepath.Join(dir, agentLockFilename)
	agentLockFDsMu.Lock()
	defer agentLockFDsMu.Unlock()
	for i, f := range agentLockFDs {
		if f.Name() != lockPath {
			continue
		}
		_ = f.Close()
		agentLockFDs = append(agentLockFDs[:i], agentLockFDs[i+1:]...)
		return
	}
}

// agentDirIsStale reports whether the agent socket directory's owner is no
// longer alive. It probes the lockfile with a non-blocking exclusive flock:
// success means the original owner has exited.
func agentDirIsStale(dir string) bool {
	lockPath := filepath.Join(dir, agentLockFilename)
	// #nosec G304 -- lockPath is derived from controlled directory listing.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	fd := int(f.Fd()) //nolint:gosec // os.File.Fd() fits in int on supported platforms
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	return true
}
