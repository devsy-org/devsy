//go:build linux

package setup

import (
	"fmt"
	"path/filepath"

	"github.com/moby/sys/mountinfo"
)

const secretEnvironmentFilesystem = "tmpfs"

func validateSecretEnvironmentMount(dir string) error {
	return validateSecretRuntimeMount(dir, "attached terminal secrets")
}

func validateSecretFileMount(dir string) error {
	return validateSecretRuntimeMount(dir, "file-mounted secrets")
}

func validateSecretRuntimeMount(dir, secretType string) error {
	cleaned := filepath.Clean(dir)
	mounts, err := mountinfo.GetMounts(mountinfo.SingleEntryFilter(cleaned))
	if err != nil {
		return fmt.Errorf("inspect %s runtime mount %s: %w", secretType, cleaned, err)
	}
	return validateSecretRuntimeMountInfo(mounts, cleaned, secretType)
}

func validateSecretEnvironmentMountInfo(mounts []*mountinfo.Info, dir string) error {
	return validateSecretRuntimeMountInfo(mounts, dir, "attached terminal secrets")
}

func validateSecretFileMountInfo(mounts []*mountinfo.Info, dir string) error {
	return validateSecretRuntimeMountInfo(mounts, dir, "file-mounted secrets")
}

func validateSecretRuntimeMountInfo(
	mounts []*mountinfo.Info,
	dir, secretType string,
) error {
	cleaned := filepath.Clean(dir)
	for _, mount := range mounts {
		if filepath.Clean(mount.Mountpoint) != cleaned {
			continue
		}
		if mount.FSType != secretEnvironmentFilesystem {
			return fmt.Errorf(
				"cannot inject %s: %s is mounted as %s, expected tmpfs; recreate the workspace",
				secretType,
				cleaned,
				mount.FSType,
			)
		}
		return nil
	}
	return fmt.Errorf(
		"cannot inject %s: %s is not a dedicated runtime mount; recreate the workspace",
		secretType,
		cleaned,
	)
}
