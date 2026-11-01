// Command ci is the Go implementation of the PR workflow's scripting.
//
// The workflow (.github/workflows/pr-ci.yml) only wires GitHub Actions
// together. What tests exist, where they run and how they run lives in
// suites.yaml and in the subcommands here:
//
//	matrix   emit the job matrices for the workflow
//	unit     run the unit-test tier on this machine
//	e2e      prepare and run Ginkgo suites
//	setup    host setup for suites (Docker, Podman)
//	success  fold the needed job results into the required status check
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: ci <matrix|unit|e2e|setup|success> [args]")
		return 2
	}
	switch args[0] {
	case "matrix":
		return matrixCLI(args[1:], stdout, stderr)
	case "unit":
		return unitCLI(args[1:], stdout, stderr)
	case "e2e":
		return e2eCLI(args[1:], stdout, stderr)
	case "setup":
		return setupCLI(args[1:], stdout, stderr)
	case "success":
		return successCLI(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
