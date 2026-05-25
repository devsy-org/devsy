package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/ssh"
)

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

	dir := filepath.Join(runtimeDir, fmt.Sprintf("auth-agent-conn-%s", connID))
	// #nosec G301
	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, "", fmt.Errorf("creating SSH_AUTH_SOCK dir: %w", err)
	}

	l, socketDir, err := ssh.NewAgentListener(dir)
	if err != nil {
		return nil, "", fmt.Errorf("new agent listener: %w", err)
	}

	return l, socketDir, nil
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
