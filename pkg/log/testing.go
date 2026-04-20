package log

import (
	"testing"

	"go.uber.org/zap/zaptest"
)

// InitTest replaces the package-level logger with a test logger.
// Log output is captured by t.Log() and only shown on test failure.
func InitTest(t testing.TB) {
	t.Helper()
	prev := sugar
	logger := zaptest.NewLogger(t)
	sugar = logger.Sugar()
	t.Cleanup(func() { sugar = prev })
}
