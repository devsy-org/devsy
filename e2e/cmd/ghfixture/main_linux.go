//go:build linux

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/e2e/framework/ghfixture"
)

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return ghfixture.Run(ctx, "/fixture", os.Args[1:], os.Stdout)
}

func main() { os.Exit(run()) }
