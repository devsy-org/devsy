package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/ssh"
)

const agentSocketDirPrefix = "auth-agent-conn-"

func setupAgentListener(reuseSock string) (net.Listener, string, error) {
	runtimeDir, err := config.DefaultPathManager().RuntimeDir()
	if err != nil {
		return nil, "", fmt.Errorf("runtime dir: %w", err)
	}

	// Ensure the runtime directory exists (on some systems like containers
	// it may not exist yet).
	err = os.MkdirAll(runtimeDir, 0o755) // #nosec G301
	if err != nil {
		return nil, "", fmt.Errorf("create runtime dir: %w", err)
	}

	// Check if we should create a "shared" socket to be reused by clients
	// used for browser tunnels such as openvscode, since the IDE itself doesn't create an SSH connection it uses a "backhaul" connection and uses the existing socket
	dir := ""
	if reuseSock != "" {
		dir = filepath.Join(runtimeDir, fmt.Sprintf("auth-agent-%s", reuseSock))
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		err = os.MkdirAll(dir, 0o755)
		if err != nil {
			return nil, "", fmt.Errorf("creating SSH_AUTH_SOCK dir: %w", err)
		}
	}

	l, tmpDir, err := ssh.NewAgentListener(dir)
	if err != nil {
		return nil, "", fmt.Errorf("new agent listener: %w", err)
	}

	return l, tmpDir, nil
}

// setupConnectionAgentListener creates a per-connection agent forwarding
// listener whose lifetime spans the entire SSH connection (not a single
// session). The connID should be a short hex string unique to the connection.
// The returned socketDir is the directory containing the unix socket and
// should be removed via cleanupAgentSocketDir once the connection closes.
func setupConnectionAgentListener(connID string) (net.Listener, string, error) {
	runtimeDir, err := config.DefaultPathManager().RuntimeDir()
	if err != nil {
		return nil, "", fmt.Errorf("runtime dir: %w", err)
	}

	err = os.MkdirAll(runtimeDir, 0o755) // #nosec G301
	if err != nil {
		return nil, "", fmt.Errorf("create runtime dir: %w", err)
	}

	dir := filepath.Join(runtimeDir, fmt.Sprintf("%s%s", agentSocketDirPrefix, connID))
	// #nosec G301
	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, "", fmt.Errorf("creating SSH_AUTH_SOCK dir: %w", err)
	}

	// Hold an exclusive flock on a per-directory lockfile for the lifetime
	// of this process. The kernel releases the lock on any exit (including
	// SIGKILL by docker exec when the proxy chain tears down). This is the
	// only cleanup signal that survives external process termination — the
	// connection-level ConnectionClosingCallback never fires in that case
	// because the in-container helper is killed by signal, not via SSH
	// disconnect. The startup janitor (SweepStaleAgentSockets) uses this
	// lock to detect and remove orphaned directories.
	if err := takeAgentDirLock(dir); err != nil {
		return nil, "", err
	}

	l, socketDir, err := ssh.NewAgentListener(dir)
	if err != nil {
		return nil, "", fmt.Errorf("new agent listener: %w", err)
	}

	return l, socketDir, nil
}

// SweepStaleAgentSockets walks the runtime directory and removes any
// auth-agent-conn-* directory whose owning process is no longer alive.
// Liveness is detected via a per-directory flock: the owning process holds
// an exclusive flock on the lockfile for its lifetime, so any other process
// that can successfully take the flock knows the original owner is gone.
//
// Call this at the start of every helper ssh-server process: in stdio mode
// the in-container helper is often SIGKILLed by docker exec when the proxy
// chain tears down, which skips all deferred cleanup (including
// ConnectionClosingCallback). Subsequent helper invocations sweep what the
// dying predecessor couldn't clean up.
func SweepStaleAgentSockets() {
	runtimeDir, err := config.DefaultPathManager().RuntimeDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), agentSocketDirPrefix) {
			continue
		}
		dirPath := filepath.Join(runtimeDir, e.Name())
		if !agentDirIsStale(dirPath) {
			continue
		}
		if err := os.RemoveAll(dirPath); err != nil {
			log.Debugf("sweep stale agent dir %s: %v", dirPath, err)
			continue
		}
		log.Debugf("swept stale agent socket dir: %s", dirPath)
	}
}

// cleanupAgentSocketDir removes the per-connection agent socket directory.
// Errors are logged at debug level since cleanup is best-effort.
func cleanupAgentSocketDir(path string) {
	if path == "" {
		return
	}
	err := os.RemoveAll(path)
	if err != nil {
		log.Debugf("cleanup agent socket dir %s: %v", path, err)
	}
}
