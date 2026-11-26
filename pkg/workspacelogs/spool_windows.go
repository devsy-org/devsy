//go:build windows

package workspacelogs

import "io/fs"

// Containers are Linux, so ownership is not meaningful here. Callers still get
// the symlink and permission checks.
func ownedByCurrentUser(fs.FileInfo) bool { return true }
