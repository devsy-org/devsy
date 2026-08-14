package docker

import (
	"context"
	"os"
	"os/user"
	"time"
)

func (r *DockerHelper) LingerWarning(ctx context.Context) string {
	if !r.IsPodman() {
		return ""
	}

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rootless, ok := probeRootlessPodman(cctx, r.buildCmd)
	if !ok || rootless {
		if !lingerEnabled() {
			return "rootless Podman without systemd linger: the workspace container will " +
				"stop when your login session ends. Run `loginctl enable-linger` to keep " +
				"it running after logout."
		}
	}
	return ""
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
