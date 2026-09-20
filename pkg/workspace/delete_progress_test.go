package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/require"
)

type progressDeleteClient struct {
	client.BaseWorkspaceClient
	lock     func(context.Context) error
	state    client.Status
	stopErr  error
	unlocked bool
}

func (c *progressDeleteClient) Lock(ctx context.Context) error { return c.lock(ctx) }
func (c *progressDeleteClient) Unlock()                        { c.unlocked = true }

func (c *progressDeleteClient) Status(
	context.Context,
	client.StatusOptions,
) (client.Status, error) {
	return c.state, nil
}
func (c *progressDeleteClient) Stop(context.Context, client.StopOptions) error { return c.stopErr }

func TestDeleteReportsLockBeforeWaitingAndStatusAfterLock(t *testing.T) {
	reporter := status.NewMemoryReporter()
	ctx := status.WithReporter(context.Background(), reporter)
	c := &progressDeleteClient{state: client.StatusRunning}
	err := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseDeletingWorkspace},
		func(ctx context.Context) error {
			// The outer start is already recorded when acquiring the lock.
			c.lock = func(context.Context) error {
				events := reporter.Events()
				require.Len(t, events, 2)
				require.Equal(t, "Waiting for workspace lock", events[1].Step)
				require.Equal(t, events[0].OperationID, events[1].ParentOperationID)
				return nil
			}
			unlock, state, err := checkBeforeDelete(ctx, c, DeleteOptions{})
			require.NoError(t, err)
			require.Equal(t, client.Status(client.StatusRunning), state)
			unlock()
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, c.unlocked)
	events := reporter.Events()
	require.Len(t, events, 6)
	require.Equal(t, "Checking workspace status", events[3].Step)
	require.Equal(t, status.StateSucceeded, events[5].State)
}

func TestDeleteProgressPreservesBestEffortStop(t *testing.T) {
	reporter := status.NewMemoryReporter()
	ctx := status.WithReporter(context.Background(), reporter)
	c := &progressDeleteClient{stopErr: errors.New("unresponsive")}
	// A failed stop emits its own failure but does not abort the parent delete.
	err := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseDeletingWorkspace},
		func(ctx context.Context) error {
			stopIfRunning(ctx, c, client.StatusRunning)
			return nil
		},
	)
	require.NoError(t, err)
	events := reporter.Events()
	require.Len(t, events, 4)
	require.Equal(t, status.StateFailed, events[2].State)
	require.Equal(t, status.StateSucceeded, events[3].State)
	require.Equal(t, events[0].OperationID, events[1].ParentOperationID)
}

func TestDeleteForceSkipsLockAndStatus(t *testing.T) {
	c := &progressDeleteClient{}
	unlock, _, err := checkBeforeDelete(context.Background(), c, DeleteOptions{Force: true})
	require.NoError(t, err)
	unlock()
	require.False(t, c.unlocked)
}

func TestDeleteLockFailureReportsFailure(t *testing.T) {
	reporter := status.NewMemoryReporter()
	c := &progressDeleteClient{
		lock: func(context.Context) error { return errors.New("lock failed") },
	}
	_, _, err := checkBeforeDelete(
		status.WithReporter(context.Background(), reporter),
		c,
		DeleteOptions{},
	)
	require.ErrorContains(t, err, "lock failed")
	events := reporter.Events()
	require.Len(t, events, 2)
	require.Equal(t, status.StateFailed, events[1].State)
	require.False(t, c.unlocked)
}
