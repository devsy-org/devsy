package docker

import (
	"context"
	"os/exec"
	"strings"
)

// probeRootlessPodman reports whether the podman daemon reachable via build is
// running rootless. ok is false when the probe itself failed (e.g. the daemon
// is unreachable), so callers can apply their own conservative policy rather
// than guessing. Rootless is a property of the running daemon, not the binary,
// so it must be probed and cannot be cached like DetectRuntime.
func probeRootlessPodman(
	ctx context.Context,
	build func(context.Context, ...string) *exec.Cmd,
) (rootless bool, ok bool) {
	out, err := build(ctx, "info", "--format", "{{.Host.Security.Rootless}}").Output()
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(string(out)) == "true", true
}
