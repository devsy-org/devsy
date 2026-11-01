//go:build !windows

package workspace

import (
	"context"
	"os"
	"os/signal"
	"sync"

	"golang.org/x/sys/unix"
)

func newExecWindowSizeWatcher(ctx context.Context) (<-chan os.Signal, func()) {
	windowChanges := make(chan os.Signal, 1)
	signal.Notify(windowChanges, unix.SIGWINCH)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
		case <-stop:
		}
		signal.Stop(windowChanges)
	}()

	var stopOnce sync.Once
	return windowChanges, func() {
		stopOnce.Do(func() { close(stop) })
		<-done
	}
}
