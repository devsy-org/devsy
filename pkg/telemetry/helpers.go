package telemetry

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

// HMAC(machine-id, $HOME), hex-encoded. Returns the injected distinct ID
// when DEVSY_TELEMETRY_DISTINCT_ID is set (desktop-spawned CLI).
func GetMachineID() string {
	if injected := os.Getenv(config.EnvTelemetryDistinctID); injected != "" {
		return injected
	}

	id, err := machineid.ID()
	if err != nil {
		// Random fallback prevents every failure-path user from collapsing
		// into a single HMAC bucket. Cached so the two GetMachineID calls
		// within one event agree.
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
