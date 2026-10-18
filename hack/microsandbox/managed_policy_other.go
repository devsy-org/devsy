//go:build !linux

package main

import (
	"fmt"
	"io"
	"runtime"
)

// managedPolicyE2ECLI rejects the wrapper off Linux, which needs mount namespaces and overlayfs.
func managedPolicyE2ECLI(_ []string, stderr io.Writer) int {
	return managedPolicyUnsupported(stderr)
}

// managedPolicyNamespaceCLI rejects the internal namespace stage off Linux.
func managedPolicyNamespaceCLI(_ []string, stderr io.Writer) int {
	return managedPolicyUnsupported(stderr)
}

func managedPolicyUnsupported(stderr io.Writer) int {
	_, _ = fmt.Fprintf(
		stderr,
		"managed-policy-e2e is unsupported on %s: requires Linux mount namespaces and overlayfs\n",
		runtime.GOOS,
	)
	return 2
}
