package devcontainer

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// defaultConsistency is applied to bind mounts on non-Linux hosts, where the
// file sharing layer benefits from an explicit consistency mode.
const defaultConsistency = "consistent"

func needsDefaultConsistency() bool {
	return runtime.GOOS != goosLinux
}

// getWorkspace resolves the workspace bind-mount string and the container-side
// mount folder. The mount string is empty when the config suppresses the mount.
func getWorkspace(
	workspaceFolder, workspaceID string,
	conf *config.DevContainerConfig,
) (mount, containerFolder string) {
	if conf.WorkspaceMount == nil {
		containerFolder = containerMountFolder(conf, workspaceID)
		return withDefaultConsistency(fmt.Sprintf(
			"type=bind,source=%s,target=%s",
			workspaceFolder,
			containerFolder,
		)), containerFolder
	}

	if *conf.WorkspaceMount == "" {
		return "", containerMountFolder(conf, workspaceID)
	}

	mount = withDefaultConsistency(*conf.WorkspaceMount)
	return mount, config.ParseMount(mount).Target
}

func containerMountFolder(conf *config.DevContainerConfig, workspaceID string) string {
	if conf.WorkspaceFolder != "" {
		return conf.WorkspaceFolder
	}
	return "/workspaces/" + workspaceID
}

// withDefaultConsistency adds the default consistency mode to a mount string on
// hosts that need it, unless the mount already specifies one.
func withDefaultConsistency(mount string) string {
	if !needsDefaultConsistency() || mountHasConsistency(mount) {
		return mount
	}
	return mount + ",consistency='" + defaultConsistency + "'"
}

func mountHasConsistency(mount string) bool {
	for part := range strings.SplitSeq(mount, ",") {
		if strings.HasPrefix(part, "consistency=") {
			return true
		}
	}
	return false
}

// mountSetConsistency returns the mount string with its consistency option set
// to value, replacing any existing value or appending one when absent.
func mountSetConsistency(mount, value string) string {
	// An empty mount is the "suppress the workspace mount" signal; keep it empty
	// rather than synthesizing a malformed consistency-only mount.
	if mount == "" {
		return ""
	}

	quoted := "consistency='" + value + "'"

	replaced := false
	var parts []string
	for part := range strings.SplitSeq(mount, ",") {
		if strings.HasPrefix(part, "consistency=") {
			parts = append(parts, quoted)
			replaced = true
		} else {
			parts = append(parts, part)
		}
	}
	if !replaced {
		parts = append(parts, quoted)
	}
	return strings.Join(parts, ",")
}
