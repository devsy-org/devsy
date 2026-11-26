//go:build windows

package workspace

import (
	"context"
	"os"
	"sync"
	"time"
)

func newExecWindowSizeWatcher(ctx context.Context) (<-chan os.Signal, func()) {
	windowChanges := make(chan os.Signal, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	ticker := time.NewTicker(time.Second)
	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				select {
				case windowChanges <- nil:
				default:
				}
			}
		}
	}()

	var stopOnce sync.Once
	return windowChanges, func() {
		stopOnce.Do(func() { close(stop) })
		<-done
	}
}
