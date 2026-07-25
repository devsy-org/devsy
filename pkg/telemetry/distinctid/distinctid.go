// Package distinctid computes the analytics identity for a Devsy invocation.
package distinctid

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/machineid"
	"github.com/devsy-org/devsy/pkg/util"
)

var (
	fallbackIDOnce sync.Once
	fallbackID     string
)

// Get returns the analytics distinct ID: the DEVSY_TELEMETRY_DISTINCT_ID
// override when set, otherwise HMAC(machine-id, $HOME) hex-encoded.
func Get() string {
	if injected := os.Getenv(config.EnvTelemetryDistinctID); injected != "" {
		return injected
	}

	id, err := machineid.ID()
	if err != nil {
		// Random fallback prevents every failure-path user from collapsing
		// into a single HMAC bucket. Cached so repeated calls within one
		// process agree.
		id = cachedFallbackID()
	}

	home, err := util.UserHomeDir()
	if err != nil {
		home = ""
	}

	mac := hmac.New(sha256.New, []byte(id))
	mac.Write([]byte(home))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func cachedFallbackID() string {
	fallbackIDOnce.Do(func() {
		fallbackID = randomFallbackID()
	})
	return fallbackID
}

func randomFallbackID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rand-error"
	}
	return hex.EncodeToString(b[:])
}
