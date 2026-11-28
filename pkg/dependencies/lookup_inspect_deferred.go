//go:build windows

package dependencies

import "context"

// inspectLookupFile defers every inspection until the Windows inspector lands.
func inspectLookupFile(_ context.Context, _ string) (lookupFile, error) {
	return lookupFile{}, errLookupDeferred
}
