//go:build windows

package session

import "errors"

const (
	noFollow = 0
	nonBlock = 0
)

var errWindowsUnsupported = errors.New(
	"pool session storage is not supported on windows: it has no owner-only ACL check yet")

// checkPlatform refuses every use of the store and lock until the Windows
// build can check or set an owner-only ACL.
func checkPlatform() error { return errWindowsUnsupported }

func checkDirMode(string) error { return nil }

func syncDir(string) error { return nil }
