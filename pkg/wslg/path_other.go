//go:build !windows

package wslg

func systemWSLPath() (string, error) { return "", ErrUnsupported }
