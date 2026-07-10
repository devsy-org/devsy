package docker

import (
	"context"
	"os"
	"os/user"
	"strings"
	"time"
)

// LingerWarning returns a non-empty warning when the container runtime is
// rootless Podman and systemd linger is disabled for the current user. In that
// configuration systemd reaps the user's containers when the login session
// ends (e.g. SSH disconnect, or the desktop app closing the spawning process),
// so a workspace that started successfully stops on its own — unlike Docker,
// whose system daemon keeps containers alive independently of user sessions.
//
// It returns "" when the situation does not apply or cannot be determined;
// detection never blocks workspace creation.
func (r *DockerHelper) LingerWarning(ctx context.Context) string {
	if !r.IsPodman() || !r.isRootless(ctx) {
		return ""
	}
	if lingerEnabled() {
		return ""
	}
	return "rootless Podman without systemd linger: the workspace container will " +
		"stop when your login session ends. Run `loginctl enable-linger` to keep " +
		"it running after logout (Docker does not require this)."
}

// isRootless reports whether Podman is running rootless. A detection failure is
// treated as rootless, since that is the case the warning targets and a false
// positive only adds a hint.
func (r *DockerHelper) isRootless(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := r.buildCmd(ctx, "info", "--format", "{{.Host.Security.Rootless}}").Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != "false"
}

// lingerDir is the directory where systemd maintains a marker file per user
// with linger enabled. Declared as a var so tests can override it.
var lingerDir = "/var/lib/systemd/linger"

// lingerEnabled reports whether systemd linger is enabled for the current user.
// It checks the marker file systemd maintains, avoiding a dependency on
// loginctl being on PATH. A read failure is treated as "enabled" so an
// undetectable state does not produce a spurious warning.
func lingerEnabled() bool {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return true
	}
	return userHasLinger(u.Username)
}

func userHasLinger(username string) bool {
	_, err := os.Stat(lingerDir + "/" + username)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}
