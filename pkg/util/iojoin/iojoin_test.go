package iojoin

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/util/goleaktest"
)

func TestMain(m *testing.M) {
	goleaktest.TestMain(m)
}

func immediateSide(err error) Side {
	return func() error {
		time.Sleep(5 * time.Millisecond)
		return err
	}
}

func TestJoinBothSucceed(t *testing.T) {
	t.Parallel()

	aErr, bErr := Join(immediateSide(nil), immediateSide(nil), time.Second, nil)
	if aErr != nil || bErr != nil {
		t.Fatalf("Join() = (%v, %v), want (nil, nil)", aErr, bErr)
	}
}

func TestJoinBothErrorsReported(t *testing.T) {
	t.Parallel()

	errA := errors.New("a failed")
	errB := errors.New("b failed")
	aErr, bErr := Join(immediateSide(errA), immediateSide(errB), time.Second, nil)
	if aErr != errA {
		t.Errorf("aErr = %v, want %v", aErr, errA)
	}
	if bErr != errB {
		t.Errorf("bErr = %v, want %v", bErr, errB)
	}
}

func TestJoinOnFirstFiresOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	first := immediateSide(nil)
	straggler := func() error {
		time.Sleep(20 * time.Millisecond)
		return nil
	}

	_, _ = Join(first, straggler, time.Second, func() {
		calls.Add(1)
	})

	if got := calls.Load(); got != 1 {
		t.Fatalf("onFirst called %d times, want 1", got)
	}
}

func TestJoinStragglerAbandonedAfterGrace(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	straggler := func() error {
		<-release
		return nil
	}
	defer close(release)

	grace := 15 * time.Millisecond
	start := time.Now()
	aErr, bErr := Join(immediateSide(nil), straggler, grace, nil)
	elapsed := time.Since(start)

	if aErr != nil {
		t.Errorf("aErr = %v, want nil", aErr)
	}
	if bErr != nil {
		t.Errorf("bErr = %v, want nil (straggler abandoned)", bErr)
	}
	if elapsed < grace {
		t.Errorf("elapsed = %v, want >= grace %v", elapsed, grace)
	}
	if elapsed > grace+500*time.Millisecond {
		t.Errorf("elapsed = %v, Join did not bound the straggler to grace", elapsed)
	}
}

func TestJoinOnFirstUnblocksStraggler(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	straggler := func() error {
		<-release
		return errors.New("straggler done after teardown")
	}

	var bErr error
	aErr, bErr := Join(
		immediateSide(nil),
		straggler,
		time.Second,
		func() { close(release) }, // eager teardown signals the straggler
	)

	if aErr != nil {
		t.Errorf("aErr = %v, want nil", aErr)
	}
	if bErr == nil {
		t.Fatal("bErr = nil, want straggler's error (finished within grace via onFirst)")
	}
}
