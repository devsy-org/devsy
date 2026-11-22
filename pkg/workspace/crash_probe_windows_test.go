//go:build windows

package workspace

import (
	"sync"
	"testing"
)

func TestTemporaryCrashDumpProbe(t *testing.T) {
	var mu sync.Mutex
	mu.Unlock()
}
