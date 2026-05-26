//go:build windows

package opener

// prepareInheritedListeners is a no-op on Windows: os/exec does not support
// inheriting arbitrary fds via ExtraFiles, so the helper must keep doing its
// own net.Listen. This leaves the (rare) probe-to-listen TOCTOU race in
// place on Windows for parallel `devsy up` invocations.
func prepareInheritedListeners(_ []string) (inheritedListenerSetup, error) {
	return inheritedListenerSetup{Cleanup: func() {}}, nil
}
