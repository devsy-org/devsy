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
//	scope    decide whether a push to main needs the Go, e2e and microsandbox jobs
//	ensure-tip  start a run for main's tip when an older push's run replaced it in the queue
//	success  fold the needed job results into the required status check
//	rerun-no-runner  rerun a run's failed jobs once when GitHub never assigned their macOS runner
//	cache-prune      delete caches of closed pull requests and main's go-unit caches from older weeks
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

var commands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"matrix":          matrixCLI,
	"unit":            unitCLI,
	"e2e":             e2eCLI,
	"setup":           setupCLI,
	"success":         successCLI,
	"scope":           scopeCLI,
	"ensure-tip":      ensureTipCLI,
	"lint-patch":      lintPatchCLI,
	"rerun-no-runner": rerunCLI,
	"cache-prune":     cachePruneCLI,
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(
			stderr,
			"usage: ci <matrix|unit|e2e|setup|scope|ensure-tip|success|lint-patch|rerun-no-runner|cache-prune> [args]",
		)
		return 2
	}
	command, ok := commands[args[0]]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
	return command(args[1:], stdout, stderr)
}
