package distinctid

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

func TestGetReturnsInjectedID(t *testing.T) {
	const injected = "owner-distinct-id"
	t.Setenv(config.EnvTelemetryDistinctID, injected)

	if got := Get(); got != injected {
		t.Fatalf("Get() = %q, want injected %q", got, injected)
	}
}

func TestGetDerivesStableIDWithoutInjection(t *testing.T) {
	t.Setenv(config.EnvTelemetryDistinctID, "")

	first := Get()
	if first == "" {
		t.Fatal("Get() returned empty distinct ID")
	}
	if second := Get(); second != first {
		t.Fatalf("Get() not stable across calls: %q != %q", first, second)
	}
}
