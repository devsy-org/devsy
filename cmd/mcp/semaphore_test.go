package mcp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpSemaphore_LimitsConcurrency(t *testing.T) {
	sem := newOpSemaphore(2)
	var inFlight, maxInFlight atomic.Int32

	track := func() {
		cur := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}

	done := make(chan struct{}, 5)
	for range 5 {
		go func() {
			release, err := sem.acquire(context.Background())
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				done <- struct{}{}
				return
			}
			track()
			release()
			done <- struct{}{}
		}()
	}
	for range 5 {
		<-done
	}

	if got := maxInFlight.Load(); got > 2 {
		t.Fatalf("max concurrent = %d, want <= 2", got)
	}
}

func TestOpSemaphore_ReleaseAllowsNextAcquire(t *testing.T) {
	sem := newOpSemaphore(1)
	release1, err := sem.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := sem.acquire(context.Background())
		if err != nil {
			t.Errorf("second acquire failed: %v", err)
			return
		}
		close(acquired)
		release2()
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire succeeded while first slot was held")
	case <-time.After(50 * time.Millisecond):
	}

	release1()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second acquire never succeeded after release")
	}
}

func TestOpSemaphore_AcquireRespectsContextCancel(t *testing.T) {
	sem := newOpSemaphore(1)
	release, err := sem.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = sem.acquire(ctx)
	if err == nil {
		t.Fatal("expected acquire to fail when context is cancelled while waiting")
	}
}
