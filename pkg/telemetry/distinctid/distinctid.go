// Package distinctid computes the analytics identity for a Devsy invocation.
package distinctid

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/machineid"
)

var (
	fallbackIDOnce sync.Once
	fallbackID     string
)

func Get() string {
	if injected := os.Getenv(config.EnvTelemetryDistinctID); injected != "" {
		return injected
	}

	id, err := machineid.ID()
	return derive(id, err, cachedFallbackID)
}

func derive(id string, err error, fallback func() string) string {
	id = strings.TrimSpace(id)
	if err != nil || id == "" || strings.EqualFold(id, "unknown") {
		id = fallback()
	}
	if id == "" {
		return ""
	}
	return Hash("machine", id)
}

func cachedFallbackID() string {
	fallbackIDOnce.Do(func() {
		fallbackID = randomFallbackID()
	})
	return fallbackID
}

func randomFallbackID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func Hash(domain, value string) string {
	mac := hmac.New(sha256.New, []byte("devsy-telemetry-v1"))
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
