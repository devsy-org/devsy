//go:build !linux

package main

import (
	"fmt"
	"io"
	"runtime"
)

// managedPolicyE2ECLI rejects managed-policy-e2e off Linux, which needs mount namespaces and overlayfs.
func managedPolicyE2ECLI(_ []string, stderr io.Writer) int {
	_, _ = fmt.Fprintf(
		stderr,
		"managed-policy-e2e is unsupported on %s: requires Linux mount namespaces and overlayfs\n",
		runtime.GOOS,
	)
	return 2
}

func managedPolicyNamespaceCLI(args []string, stderr io.Writer) int {
	return managedPolicyE2ECLI(args, stderr)
}
