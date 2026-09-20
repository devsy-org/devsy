package up

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func expiredContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	t.Cleanup(cancel)
	<-ctx.Done()
	return ctx
}

func TestClassifyPodmanHealthFailure(t *testing.T) {
	t.Run("deadline exceeded means wedged daemon", func(t *testing.T) {
		assert.Equal(
			t,
			podmanHealthTimeout,
			classifyPodmanHealthFailure(expiredContext(t), ""),
		)
	})

	unavailableOutputs := []string{
		"Error: cannot connect to the Podman socket",
		"dial unix /run/podman/podman.sock: connect: connection refused",
		"fork/exec /usr/local/bin/podman: no such file or directory",
	}
	for _, output := range unavailableOutputs {
		t.Run("socket problem means unavailable: "+output, func(t *testing.T) {
			assert.Equal(
				t,
				podmanHealthUnavailable,
				classifyPodmanHealthFailure(context.Background(), output),
			)
		})
	}

	t.Run("responsive daemon error stays an error", func(t *testing.T) {
		assert.Equal(
			t,
			podmanHealthError,
			classifyPodmanHealthFailure(
				context.Background(),
				"Error: statfs /var/lib/containers: permission denied",
			),
		)
	})

	t.Run("empty output without deadline stays an error", func(t *testing.T) {
		assert.Equal(
			t,
			podmanHealthError,
			classifyPodmanHealthFailure(context.Background(), ""),
		)
	})
}

func TestShouldAttemptPodmanRecovery(t *testing.T) {
	assert.True(t, shouldAttemptPodmanRecovery(podmanHealthTimeout))
	assert.True(t, shouldAttemptPodmanRecovery(podmanHealthUnavailable))
	assert.False(t, shouldAttemptPodmanRecovery(podmanHealthError))
	assert.False(t, shouldAttemptPodmanRecovery(podmanHealthOK))
}

func TestPodmanDaemonGateRecordsFirstFailure(t *testing.T) {
	gate := &podmanDaemonGate{}
	assert.Empty(t, gate.unhealthy())
	gate.markUnhealthy("first spec")
	gate.markUnhealthy("second spec")
	assert.Equal(t, "first spec", gate.unhealthy())
}

func TestPodmanHealthClassString(t *testing.T) {
	assert.Equal(t, "ok", podmanHealthOK.String())
	assert.Equal(t, "timeout", podmanHealthTimeout.String())
	assert.Equal(t, "unavailable", podmanHealthUnavailable.String())
	assert.Equal(t, "error", podmanHealthError.String())
	assert.Equal(t, "unknown", podmanHealthClass(99).String())
}
