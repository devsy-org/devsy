//go:build windows

package ssh

import (
	"context"
	"os"
	"time"
)

func WatchWindowSize(ctx context.Context) <-chan os.Signal {
	windowSize := make(chan os.Signal, 3)
	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			select {
			case windowSize <- nil:
			case <-ctx.Done():
				return
			}
		}
	}()
	return windowSize
}
