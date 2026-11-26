package microsandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/require"
)

const recreateTestVersion = "0.7.7"

func TestRejectedReplacementKeepsExistingSandbox(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
			client := newFakeClient()
			client.version = recreateTestVersion
			original := &sandboxInfo{Name: wsName, Running: running, CreatedAt: time.Unix(123, 0)}
			client.info[wsName] = original
			rejected := errors.New("invalid final effective configuration")
			client.failCreat = rejected
			err := newDriver(client, nil, specDefaults{}).RunDevContainer(t.Context(), wsID,
				&driver.RunOptions{Image: imgX, AllowRecreate: true})
			require.ErrorIs(t, err, rejected)
			require.Same(t, original, client.info[wsName])
			require.Equal(t, running, original.Running)
			for _, call := range client.calls {
				require.False(
					t,
					strings.HasPrefix(call, "stop:") || strings.HasPrefix(call, "remove:"),
					call,
				)
			}
		})
	}
}

func TestRecreationVersionGateAppliesWithOwnershipOff(t *testing.T) {
	for _, version := range []string{"0.7.2", "0.7.6", "0.7.7-rc.1", "unknown"} {
		t.Run(version, func(t *testing.T) {
			client := newFakeClient()
			client.version = version
			client.failPrepare = errors.New("image preparation must not run")
			d := newDriver(client, nil, specDefaults{})
			d.workspaceMountPolicy.StatVirtualization = statVirtOff
			err := d.RunDevContainer(
				t.Context(),
				wsID,
				&driver.RunOptions{Image: imgX, AllowRecreate: true},
			)
			var preflight *driver.PreflightError
			require.ErrorAs(t, err, &preflight)
			require.NotErrorIs(t, err, client.failPrepare)
			require.Empty(t, client.calls)
			require.Empty(t, client.created)
		})
	}
}

func TestRecreationPreflightPreservesProbeFailureAndCancellation(t *testing.T) {
	client := newFakeClient()
	client.failVersion = errors.New("owned version probe failed")
	d := newDriver(client, nil, specDefaults{})
	require.ErrorIs(t, d.recreationPreflight(t.Context()), client.failVersion)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, d.recreationPreflight(ctx), context.Canceled)
	require.Equal(t, 1, client.versionCalls)
}
