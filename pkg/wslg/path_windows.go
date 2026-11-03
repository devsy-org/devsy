package wslg

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func systemWSLPath() (string, error) {
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", ErrExecutable
	}
	path := filepath.Join(directory, "wsl.exe")
	if !filepath.IsAbs(path) {
		return "", ErrExecutable
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", ErrExecutable
	}
	return path, nil
}
