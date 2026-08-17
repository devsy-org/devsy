// Package goleaktest provides a small helper for wiring go.uber.org/goleak
// into a package's TestMain so goroutine leaks fail the test suite.
//
// Usage in a package's <pkg>_test.go:
//
//	func TestMain(m *testing.M) {
//	    goleaktest.TestMain(m)
//	}
//
// Tests that intentionally leave a goroutine running should clean it up. If
// that is impossible, register an ignore via goleak.IgnoreAnyFunction(...) and
// pass it through Options.Extra.
package goleaktest

import (
	"testing"

	"go.uber.org/goleak"
)

// Options holds goleak options for packages that need to ignore known,
// long-lived goroutines.
type Options struct {
	// IgnoreCurrent ignores all goroutines that are running at the time of the call.
	IgnoreCurrent bool
	// Extra appends additional goleak.Option values.
	Extra []goleak.Option
}

// Run wires goleak verification around the package's test suite. It runs the
// tests and, if they pass, asserts no goroutines leaked.
func Run(m *testing.M, opts Options) {
	optsSlice := make([]goleak.Option, 0, len(opts.Extra)+1)
	if opts.IgnoreCurrent {
		optsSlice = append(optsSlice, goleak.IgnoreCurrent())
	}
	optsSlice = append(optsSlice, opts.Extra...)

	goleak.VerifyTestMain(m, optsSlice...)
}

func TestMain(m *testing.M) {
	Run(m, Options{})
}
