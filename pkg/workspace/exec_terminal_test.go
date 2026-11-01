package workspace

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestForwardExecWindowChangesUsesCurrentDimensions(t *testing.T) {
	ctx := context.Background()
	windowChanges := make(chan os.Signal, 2)
	windowSizes := [][2]int{{100, 35}, {132, 44}}
	var sizeIndex int
	gotChanges := make(chan [2]int, len(windowSizes))
	stop := forwardExecWindowChanges(
		ctx,
		windowChanges,
		func() (int, int, error) {
			size := windowSizes[sizeIndex]
			sizeIndex++
			return size[0], size[1], nil
		},
		func(height, width int) error {
			gotChanges <- [2]int{height, width}
			return nil
		},
	)

	windowChanges <- os.Interrupt
	if got := receiveWindowChange(t, gotChanges); got != [2]int{35, 100} {
		t.Fatalf("first window change = %v, want height 35, width 100", got)
	}
	windowChanges <- os.Interrupt
	if got := receiveWindowChange(t, gotChanges); got != [2]int{44, 132} {
		t.Fatalf("second window change = %v, want height 44, width 132", got)
	}

	stop()
	stop()
	windowChanges <- os.Interrupt
	select {
	case got := <-gotChanges:
		t.Fatalf("forwarder sent a resize after cleanup: %v", got)
	default:
	}
}

func TestExecWindowSizeWatcherStopsAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, stop := newExecWindowSizeWatcher(ctx)

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("window-size watcher did not stop")
	}
}

func TestForwardExecWindowChangesStopsOnParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	windowChanges := make(chan os.Signal, 1)
	var sends int
	stop := forwardExecWindowChanges(
		ctx,
		windowChanges,
		func() (int, int, error) { return 90, 30, nil },
		func(int, int) error {
			sends++
			return nil
		},
	)

	cancel()
	windowChanges <- os.Interrupt
	stop()
	if sends != 0 {
		t.Fatalf("forwarder sent %d resize after parent cancellation, want 0", sends)
	}
}

func receiveWindowChange(t *testing.T, changes <-chan [2]int) [2]int {
	t.Helper()
	select {
	case change := <-changes:
		return change
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for window change")
		return [2]int{}
	}
}
