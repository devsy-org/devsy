// iojoin provides a utility for joining two concurrent units of work that share
// resources. It is used to implement the duplex bridge between the caller's stdio
// and the exec's stdio.
package iojoin

import "time"

// Side is a concurrent, error-reporting unit of work that can be joined with
// another Side. It is expected to be run in its own goroutine.
type Side func() error

// Join runs two sides concurrently and returns once both have completed or the
// slower side is abandoned after grace. The first side to complete fires the
// onFirst hook, which is where the caller should tear down shared resources
// (close pipe ends, cancel a context) so the straggler can finish naturally.
// The caller owns the resources and must close them; Join does not close any of
// its arguments.
func Join(a, b Side, grace time.Duration, onFirst func()) (aErr, bErr error) {
	aDone := make(chan error, 1)
	bDone := make(chan error, 1)

	go func() { aDone <- a() }()
	go func() { bDone <- b() }()

	var firstErr error
	var other <-chan error
	select {
	case firstErr = <-aDone:
		other = bDone
		aErr = firstErr
	case firstErr = <-bDone:
		other = aDone
		bErr = firstErr
	}

	if onFirst != nil {
		onFirst()
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case secondErr := <-other:
		if other == bDone {
			bErr = secondErr
		} else {
			aErr = secondErr
		}
	case <-timer.C:
	}

	return aErr, bErr
}
