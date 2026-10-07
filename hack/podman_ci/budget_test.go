package main

import (
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time        { return c.now }
func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

func TestBudgetBoundsAndReserves(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	budget, err := NewBudget(270*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.Sleep(100001 * time.Millisecond)
	if got := budget.Bound(150*time.Second, 85*time.Second); got != 84999*time.Millisecond {
		t.Fatalf("Bound() = %s", got)
	}
	clock.Sleep(84999 * time.Millisecond)
	if got := budget.Remaining(85 * time.Second); got != 0 {
		t.Fatalf("Remaining() = %s, want 0", got)
	}
}

func TestBudgetRejectsNonPositiveTimeout(t *testing.T) {
	if _, err := NewBudget(0, nil); err == nil {
		t.Fatal("NewBudget accepted zero timeout")
	}
}
