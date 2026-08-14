package docker

import (
	"context"
	"os/exec"
	"strings"
)

// probeRootlessPodman reports whether the podman daemon reachable via build is
// running rootless. If the probe succeeds, ok is true and rootless indicates
// whether the daemon is rootless. If the probe fails, ok is false and rootless is false.
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
