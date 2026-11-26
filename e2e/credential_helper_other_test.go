//go:build !linux

package e2e

func dispatchFixtureCredentialHelper() (bool, int) {
	return false, 0
}
