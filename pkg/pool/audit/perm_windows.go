//go:build windows

package audit

import "errors"

const (
	noFollow = 0
	nonBlock = 0
)

var errWindowsUnsupported = errors.New(
	"audit storage is not supported on windows: it has no owner-only ACL check yet")

// checkPlatform refuses every use of the store. The Windows build cannot
// inspect or set the directory ACL, so it cannot promise that other users
// cannot write the audit records.
func checkPlatform() error { return errWindowsUnsupported }

func ownedByCurrentUser(string) bool { return false }

func checkDirMode(string) error { return nil }

// syncDir does nothing on Windows, which cannot flush a directory handle.
func syncDir(string) error { return nil }
