package docker

import (
	"context"
	"os"
	"os/user"
	"strings"
	"time"
)

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

func (r *DockerHelper) isRootless(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := r.buildCmd(ctx, "info", "--format", "{{.Host.Security.Rootless}}").Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != "false"
}

var lingerDir = "/var/lib/systemd/linger"

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
