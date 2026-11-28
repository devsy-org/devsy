//go:build windows

package channel

// checkPrivateDir has no mode bits to check on Windows, where access follows
// the directory's ACL.
func checkPrivateDir(string) error { return nil }
