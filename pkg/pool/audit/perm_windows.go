//go:build windows

package audit

const noFollow = 0

// checkDirMode does not inspect the directory ACL on Windows, so Open accepts
// a directory that other users can write to. Pool must not run on Windows
// until the audit directory gets an owner-only ACL or startup refuses.
func checkDirMode(string) error { return nil }

// syncDir does nothing on Windows, which cannot flush a directory handle.
func syncDir(string) error { return nil }
