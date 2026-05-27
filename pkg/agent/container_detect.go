package agent

import (
	"errors"
	"os"
	"strings"
)

// Container indicator paths. These are the well-known filesystem markers
// that a Linux container runtime drops into the rootfs. None of them
// exist on macOS or Windows hosts, and a non-containerized Linux host
// will not have them either.
const (
	dockerEnvPath         = "/.dockerenv"
	podmanEnvPath         = "/run/.containerenv"
	cgroupPath            = "/proc/1/cgroup"
	cgroupDockerToken     = "docker"
	cgroupContainerdToken = "containerd"
)

// statExistsFn is the dependency-injection seam for filesystem existence
// checks. Production wires it to a thin os.Stat wrapper; tests provide a
// table-driven fake.
type statExistsFn func(path string) (bool, error)

// readFileFn is the dependency-injection seam for reading the cgroup
// file. Production wires it to os.ReadFile; tests provide a fake.
type readFileFn func(path string) ([]byte, error)

// defaultStatExists reports whether `path` exists. ENOENT is treated as
// "no, but not an error" so callers don't have to discriminate.
func defaultStatExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// defaultReadFile reads `path`. Missing files yield (nil, nil) so the
// caller can treat them the same as "no container indicator".
func defaultReadFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- fixed allowlist of /proc paths
	if err == nil {
		return b, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, err
}

// isLikelyContainer reports whether the current process appears to be
// running inside a Linux container. It checks three independent
// indicators; any one of them is sufficient.
//
// Host platforms (macOS, Windows, non-containerized Linux) will see all
// three checks fail and the function returns false.
func isLikelyContainer() bool {
	return isLikelyContainerWith(defaultStatExists, defaultReadFile)
}

// isLikelyContainerWith is the testable core of isLikelyContainer.
func isLikelyContainerWith(stat statExistsFn, read readFileFn) bool {
	for _, marker := range []string{dockerEnvPath, podmanEnvPath} {
		if ok, _ := stat(marker); ok {
			return true
		}
	}
	data, _ := read(cgroupPath)
	if len(data) == 0 {
		return false
	}
	content := string(data)
	return strings.Contains(content, cgroupDockerToken) ||
		strings.Contains(content, cgroupContainerdToken)
}
