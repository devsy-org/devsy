package dependencies

import "context"

// inspectLookupFile defers every inspection until the platform inspectors land.
func inspectLookupFile(_ context.Context, _ string) (lookupFile, error) {
	return lookupFile{}, errLookupDeferred
}
