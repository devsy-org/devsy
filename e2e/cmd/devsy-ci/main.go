// Command devsy-ci provisions and verifies the runtimes the E2E suite runs on.
//
// It is the composition root of the E2E CI helpers: it builds the real
// runenv.Dependencies and wires the e2erun and dockerwsl commands to them. All
// behaviour lives in the e2e/framework packages.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

func main() { os.Exit(run()) }

// run owns the process exit status so deferred cleanup runs before exit.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCommand(runenv.NewDefaultDependencies())
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, runenv.Redact(err.Error()))
		return runenv.ExitCodeForError(err)
	}
	return 0
}
