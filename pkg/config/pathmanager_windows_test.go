//go:build windows

package config

import (
	"path/filepath"
)

func readOnlyPlatformTestRoots(home, temp string) readOnlyTestRoots {
	data := filepath.Join(home, "AppData", "Local", RepoName)
	return readOnlyTestRoots{
		config: filepath.Join(home, "AppData", "Roaming", RepoName),
		data:   data, cache: filepath.Join(data, "cache"), state: filepath.Join(data, "state"),
		runtime:   filepath.Join(temp, RepoName),
		systemBin: filepath.Join(home, "AppData", "Local", "Microsoft", "WindowsApps"),
	}
}
