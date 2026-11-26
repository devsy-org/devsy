//go:build unix && !linux && !darwin

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func readOnlyPlatformTestRoots(home, temp string) readOnlyTestRoots {
	data := filepath.Join(home, "."+RepoName)
	return readOnlyTestRoots{
		config: data,
		data:   data,
		cache:  filepath.Join(home, ".cache", RepoName),
		state: filepath.Join(
			data,
			"state",
		),
		runtime:   filepath.Join(temp, fmt.Sprintf("%s-%d", RepoName, os.Getuid())),
		systemBin: "/usr/local/bin",
	}
}
