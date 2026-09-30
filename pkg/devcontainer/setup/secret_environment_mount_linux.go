//go:build linux

package setup

import (
	"fmt"
	"path/filepath"

	"github.com/moby/sys/mountinfo"
)

const secretEnvironmentFilesystem = "tmpfs"

func validateSecretEnvironmentMount(dir string) error {
	cleaned := filepath.Clean(dir)
	mounts, err := mountinfo.GetMounts(mountinfo.SingleEntryFilter(cleaned))
	if err != nil {
		return fmt.Errorf("inspect terminal secret runtime mount %s: %w", cleaned, err)
	}
	return validateSecretEnvironmentMountInfo(mounts, cleaned)
}

func validateSecretEnvironmentMountInfo(mounts []*mountinfo.Info, dir string) error {
	cleaned := filepath.Clean(dir)
	for _, mount := range mounts {
		if filepath.Clean(mount.Mountpoint) != cleaned {
			continue
		}
		if mount.FSType != secretEnvironmentFilesystem {
			return fmt.Errorf(
				"cannot inject attached terminal secrets: %s is mounted as %s, expected tmpfs; recreate the workspace",
				cleaned,
				mount.FSType,
			)
		}
		return nil
	}
	return fmt.Errorf(
		"cannot inject attached terminal secrets: %s is not a dedicated runtime mount; recreate the workspace",
		cleaned,
	)
}
