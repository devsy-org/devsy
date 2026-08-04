//go:build windows

package provider

// syncDir is a no-op on Windows; see atomic_posix.go for the rationale.
func syncDir(_ string) error {
	return nil
}
