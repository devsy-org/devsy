package up

import (
	"context"
	"errors"
	"testing"

	client2 "github.com/devsy-org/devsy/pkg/client"
)

// statusOnlyClient answers Status and panics on any other method through the
// embedded nil interface, so a health probe that reaches for a mutating path
// fails the test.
type statusOnlyClient struct {
	client2.BaseWorkspaceClient
	status client2.Status
	err    error
}

func (c *statusOnlyClient) Status(
	context.Context,
	client2.StatusOptions,
) (client2.Status, error) {
	return c.status, c.err
}

func TestWorkspaceTunnelHealthHealthyOnlyWhenRunning(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  client2.Status
		err     error
		healthy bool
	}{
		{"running", client2.StatusRunning, nil, true},
		{"stopped", client2.StatusStopped, nil, false},
		{"not found", client2.StatusNotFound, nil, false},
		{"provisioning", client2.StatusProvisioning, nil, false},
		{"failed", client2.StatusFailed, nil, false},
		{"busy", client2.StatusBusy, nil, false},
		{"status error", "", errors.New("daemon unreachable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			health := workspaceTunnelHealth(&statusOnlyClient{status: tc.status, err: tc.err})
			err := health(t.Context())
			if tc.healthy && err != nil {
				t.Errorf("health = %v, want nil", err)
			}
			if !tc.healthy && err == nil {
				t.Error("health = nil, want failure")
			}
		})
	}
}
