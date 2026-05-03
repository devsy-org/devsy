//go:build windows

package config

import "fmt"

func availableStorageBytes(path string) (uint64, error) {
	return 0, fmt.Errorf("storage detection not supported on windows")
}
