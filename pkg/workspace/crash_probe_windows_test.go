//go:build windows

package workspace

import (
	"sync"
	"testing"
)

func TestProviderUpdateTemporaryCrashProbe(t *testing.T) {
	var mu sync.Mutex
	mu.Unlock()
}
