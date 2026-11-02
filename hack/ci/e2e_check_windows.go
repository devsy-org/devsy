//go:build windows

package main

// requireOwnedBy is a no-op on Windows, where Unix ownership does not apply.
func requireOwnedBy(_ string, _ int) error { return nil }
