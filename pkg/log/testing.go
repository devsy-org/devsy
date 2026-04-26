package log

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

// InitTest replaces the package-level logger with a test logger.
// Log output is captured by t.Log() and only shown on test failure.
func InitTest(t testing.TB) {
	t.Helper()
	prev := sugar.Load()
	logger := zaptest.NewLogger(t)
	sugar.Store(logger.Sugar())
	t.Cleanup(func() { sugar.Store(prev) })
}

// InitTestObserved replaces the package-level logger with an observable
// logger at the given level. The returned ObservedLogs can be used to
// assert that specific log messages were emitted.
func InitTestObserved(t testing.TB, level zapcore.Level) *observer.ObservedLogs {
	t.Helper()
	prev := sugar.Load()
	core, logs := observer.New(level)
	sugar.Store(zap.New(core).Sugar())
	t.Cleanup(func() { sugar.Store(prev) })
	return logs
}
