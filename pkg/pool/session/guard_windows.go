//go:build windows

package session

import (
	"errors"
	"os"
)

const nonBlock = 0

var errWindowsUnsupported = errors.New(
	"pool session storage is not supported on windows: it has no owner-only ACL check yet")

// checkPlatform refuses every use of the store and lock until the Windows
// build can check or set an owner-only ACL.
func checkPlatform() error { return errWindowsUnsupported }

func tryLock(*os.File) (bool, error) { return false, errWindowsUnsupported }

func checkRoot(*os.Root) error { return nil }

func syncDirectory(*os.Root) error { return nil }
