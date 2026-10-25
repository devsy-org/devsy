//go:build !windows

package agentcontainer

import (
	"context"
	"errors"
	"fmt"
	"time"

	config2 "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const (
	// ownerWaitTimeout bounds how long setup waits for the managed daemon when a
	// positive timeout depends on it.
	ownerWaitTimeout = 30 * time.Second
	// optionalOwnerWait bounds the wait when no timeout is configured.
	optionalOwnerWait = time.Second
)

// lifecycleHandshake is setup's side of the managed daemon protocol.
type lifecycleHandshake struct {
	sender         lifecycleSender
	timeout        string
	shutdownAction string
}

type lifecycleSender interface {
	Send(state agentd.LifecycleState, timeout, shutdownAction string) error
}

func isManagedLifecycle(info *provider2.ContainerWorkspaceInfo) bool {
	return info.LifecycleOwner == provider2.ContainerLifecycleOwnerManagedDaemon
}

// beginLifecycle connects to the container's managed daemon and suspends its
// timer for the duration of setup. It returns nil when there is no handshake to
// do: not a managed container, or no timeout and no daemon to update.
func beginLifecycle(
	ctx context.Context,
	dir string,
	info *provider2.ContainerWorkspaceInfo,
	shutdownAction string,
) (*lifecycleHandshake, error) {
	if !isManagedLifecycle(info) {
		return nil, nil
	}
	wait := ownerWaitTimeout
	if info.ContainerTimeout == "" {
		wait = optionalOwnerWait
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	client, err := agentd.ConnectLifecycle(waitCtx, dir)
	if err != nil {
		if info.ContainerTimeout == "" && errors.Is(err, agentd.ErrLifecycleOwnerMissing) {
			log.Debugf("no container lifecycle owner and no inactivity timeout configured")
			return nil, nil
		}
		return nil, migrationError(err)
	}

	h := &lifecycleHandshake{
		sender:         client,
		timeout:        info.ContainerTimeout,
		shutdownAction: shutdownAction,
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
	if err := h.sender.Send(state, h.timeout, h.shutdownAction); err != nil {
		return fmt.Errorf("update container lifecycle (%s): %w", state, err)
	}
	return nil
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

// startWorkerActivity records activity for the lifetime of a background setup
// worker when a managed daemon owns the timer. The returned stop function ends
// the writer and leaves a fresh timestamp behind.
func startWorkerActivity(ctx context.Context) (stop func()) {
	timeout, ok := agentd.ReadLifecycleTimeout(config2.ContainerLifecycleDir)
	if !ok {
		return func() {}
	}
	return agentd.StartActivityWriter(ctx, agentd.HeartbeatInterval(timeout))
}
