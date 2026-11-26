//go:build !windows

package clientimplementation

import (
	"os"
	"path/filepath"
)

func createProviderInvocationDir() (string, error) {
	dir, err := os.MkdirTemp("", "devsy-provider-invocation-")
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	return absolute, nil
}
