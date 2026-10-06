package up

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.True(t, gate.claimRecovery())
	assert.False(t, gate.claimRecovery())
}

func TestPodmanHealthClassString(t *testing.T) {
	assert.Equal(t, "ok", podmanHealthOK.String())
	assert.Equal(t, "timeout", podmanHealthTimeout.String())
	assert.Equal(t, "unavailable", podmanHealthUnavailable.String())
	assert.Equal(t, "error", podmanHealthError.String())
	assert.Equal(t, "poisoned", podmanHealthPoisoned.String())
	assert.Equal(t, "unknown", podmanHealthClass(99).String())
}

func TestRunDiagCommandRespectsParentBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Contains(t, runDiagCommand(ctx, "sh", "-c", "sleep 10"), "command failed")
}

func TestRunPodmanDiagnosticsStopsAtBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var commands []string
	runPodmanDiagnostics(ctx, []podmanDiagSection{
		{"processes", "ps", nil},
		{"podman", "podman", nil},
	}, func(_ context.Context, name string, _ ...string) string {
		commands = append(commands, name)
		cancel()
		return "diagnostic failed"
	}, func(_, _ string) {})
	assert.Equal(t, []string{"ps"}, commands)
}

func TestRootfulPodmanWrapper(t *testing.T) {
	endpoint := "unix:///run/podman/podman.sock"
	emptyEndpoint := ""
	cases := []struct {
		name       string
		endpoint   *string
		wantPrefix []string
		args       []string
	}{
		{
			name:       "configured endpoint",
			endpoint:   &endpoint,
			wantPrefix: []string{"podman", "--remote", "--url", endpoint},
			args:       []string{"ps", "-a"},
		},
		{
			name:       "preserves arguments",
			endpoint:   &endpoint,
			wantPrefix: []string{"podman", "--remote", "--url", endpoint},
			args:       []string{"run", "value with spaces", "$(printf unsafe)"},
		},
		{
			name:       "unset endpoint uses local mode",
			wantPrefix: []string{"podman"},
			args:       []string{"ps", "-a"},
		},
		{
			name:       "empty endpoint uses local mode",
			endpoint:   &emptyEndpoint,
			wantPrefix: []string{"podman"},
			args:       []string{"ps", "-a"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runRootfulPodmanWrapper(t, tc.endpoint, tc.args...)
			require.Equal(t, append(tc.wantPrefix, tc.args...), got)
			require.NotContains(t, got, "-E")
		})
	}
}

func runRootfulPodmanWrapper(t *testing.T, endpoint *string, args ...string) []string {
	t.Helper()

	dir := t.TempDir()
	capturePath := filepath.Join(dir, "argv")
	sudoPath := filepath.Join(dir, "sudo")
	sudoScript := `#!/bin/sh
printf '%s\0' "$@" > "$CAPTURE_FILE"
	`
	require.NoError(t, os.WriteFile(sudoPath, []byte(sudoScript), 0o600))
	//nolint:gosec // G302: test executable needs owner execute permission.
	require.NoError(t, os.Chmod(sudoPath, 0o700))

	wrapperPath := filepath.Join(dir, "podman-rootful")
	require.NoError(t, os.WriteFile(wrapperPath, []byte(rootfulPodmanWrapperScript()), 0o600))
	//nolint:gosec // G302: generated wrapper needs owner execute permission.
	require.NoError(t, os.Chmod(wrapperPath, 0o700))

	//nolint:gosec // G204: generated wrapper path is test-controlled.
	cmd := exec.Command(wrapperPath, args...)
	cmd.Env = []string{"PATH=" + dir, "CAPTURE_FILE=" + capturePath}
	if endpoint != nil {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+*endpoint)
	}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	argv, err := os.ReadFile(capturePath) //nolint:gosec // G304: test-controlled capture path.
	require.NoError(t, err)
	encodedArgs := bytes.Split(argv, []byte{0})
	encodedArgs = encodedArgs[:len(encodedArgs)-1]
	got := make([]string, len(encodedArgs))
	for i, arg := range encodedArgs {
		got[i] = string(arg)
	}
	return got
}
