package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() { os.Exit(run()) }

// run owns the process exit status so deferred cleanup runs before exit.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCommand(newDefaultDependencies())
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, redact(err.Error()))
		return exitCodeForError(err)
	}
	return 0
}
