//go:build !windows

package agentcontainer

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const (
	// ownerWaitTimeout bounds managed-owner startup, even with no timer.
	ownerWaitTimeout = 30 * time.Second
)

// lifecycleHandshake is setup's side of the managed daemon protocol.
type lifecycleHandshake struct {
	client         *agentd.LifecycleClient
	ctx            context.Context
	cancel         context.CancelCauseFunc
	timeout        string
	shutdownAction string
}

func isManagedLifecycle(info *provider2.ContainerWorkspaceInfo) bool {
	return info.LifecycleOwner == provider2.ContainerLifecycleOwnerManagedDaemon
}

// beginLifecycle requires an acknowledged managed owner before setup can run.
// Unmanaged containers do not participate in this protocol.
func beginLifecycle(
	ctx context.Context,
	dir string,
	info *provider2.ContainerWorkspaceInfo,
	shutdownAction string,
) (*lifecycleHandshake, error) {
	if !isManagedLifecycle(info) {
		return nil, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, ownerWaitTimeout)
	defer cancel()

	client, err := agentd.ConnectLifecycle(waitCtx, dir)
	if err != nil {
		return nil, migrationError(err)
	}

	workCtx, stop := client.ProtectedContext(ctx)
	h := &lifecycleHandshake{
		client: client, ctx: workCtx, cancel: stop,
		timeout: info.ContainerTimeout, shutdownAction: shutdownAction,
	}
	if err := h.send(agentd.LifecycleStateSetup); err != nil {
		return nil, err
	}
	return h, nil
}

func migrationError(err error) error {
	return fmt.Errorf(
		"this container's main process does not support the Devsy inactivity lifecycle (%w); "+
			"stop and start the workspace once (devsy workspace stop, then devsy up) to replace "+
			"the pod. The workspace volume is kept; data written to the container filesystem "+
			"outside mounted volumes is not", err)
}

func (h *lifecycleHandshake) send(state agentd.LifecycleState) error {
	if err := h.client.Send(state, h.timeout, h.shutdownAction); err != nil {
		h.cancel(err)
		_ = h.client.Close()
		return fmt.Errorf("update container lifecycle (%s): %w", state, err)
	}
	return nil
}

func (h *lifecycleHandshake) close() {
	h.cancel(context.Canceled)
	_ = h.client.Close()
}

func (h *lifecycleHandshake) finish(err error) error {
	if err == nil {
		return nil
	}
	h.fail()
	if cause := context.Cause(h.ctx); errors.Is(cause, agentd.ErrLifecycleOwnerMissing) {
		return migrationError(cause)
	}
	return err
}

// ready resets activity and arms the timer.
func (h *lifecycleHandshake) ready() error {
	return h.send(agentd.LifecycleStateReady)
}

// fail releases the suspension so a failed setup cannot leave the timer stuck.
func (h *lifecycleHandshake) fail() {
	if err := h.send(agentd.LifecycleStateFailed); err != nil {
		log.Errorf("release container lifecycle suspension: %v", err)
	}
}

// startWorkerActivity completes managed registration before starting hooks.
func startWorkerActivity(ctx context.Context) (context.Context, func(), error) {
	return agentd.StartActivityWriter(ctx, agentd.DefaultActivityInterval)
}
