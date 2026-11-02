//go:build !windows

package agentcontainer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const (
	// ownerWaitTimeout bounds managed-owner startup, even with no timer.
	ownerWaitTimeout = 30 * time.Second

	// envWorkerReadyFile names the file a hook worker creates once it has registered.
	envWorkerReadyFile = "DEVSY_WORKER_READY_FILE"
	// workerReadyTimeout bounds how long setup waits for a spawned worker to register.
	workerReadyTimeout = 10 * time.Second
	workerReadyPoll    = 50 * time.Millisecond
	workerReadyDirMode = 0o700

	workerReadyDirPrefix = "devsy-worker-ready-"
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
			"stop and start the workspace once (devsy workspace stop, then devsy workspace up) "+
			"to replace the pod. The workspace volume is kept; data written to the "+
			"container filesystem outside mounted volumes is not", err)
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
	ctx, stop, err := agentd.StartActivityWriter(ctx, agentd.DefaultActivityInterval)
	if err == nil {
		signalWorkerReady()
	}
	return ctx, stop, err
}

// workerReadiness lets setup keep its own protection until the hook workers it
// spawned have registered theirs. Methods are no-ops on a nil receiver, which
// is how unmanaged containers skip the handshake.
type workerReadiness struct {
	dir     string
	spawned []string
}

func newWorkerReadiness() (*workerReadiness, error) {
	dir, err := os.MkdirTemp("", workerReadyDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create worker readiness dir: %w", err)
	}
	if err := os.Chmod(dir, workerReadyDirMode); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("secure worker readiness dir: %w", err)
	}
	return &workerReadiness{dir: dir}, nil
}

// launch runs start, which receives a function that tells the worker about to
// be spawned where to report registration. The worker is awaited only when
// start succeeds and actually built a command, so a failed or skipped launch
// cannot stall setup for a marker that will never arrive.
func (w *workerReadiness) launch(name string, start func(prepare func(*exec.Cmd)) error) error {
	built := false
	err := start(func(cmd *exec.Cmd) {
		built = true
		if w == nil {
			return
		}
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, envWorkerReadyFile+"="+filepath.Join(w.dir, name))
	})
	if err == nil && built && w != nil {
		w.spawned = append(w.spawned, name)
	}
	return err
}

// wait blocks until every prepared worker has registered or the bound elapses.
func (w *workerReadiness) wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, workerReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(workerReadyPoll)
	defer ticker.Stop()
	for _, name := range w.spawned {
		for {
			if _, err := os.Stat(filepath.Join(w.dir, name)); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("hook worker %s did not register: %w", name, context.Cause(ctx))
			case <-ticker.C:
			}
		}
	}
	return nil
}

func (w *workerReadiness) close() {
	if w != nil {
		_ = os.RemoveAll(w.dir)
	}
}

// signalWorkerReady reports registration to the setup that spawned this worker.
func signalWorkerReady() {
	path := os.Getenv(envWorkerReadyFile)
	if path == "" {
		return
	}
	// Hooks inherit this process's env and have no use for the path.
	_ = os.Unsetenv(envWorkerReadyFile)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		!strings.HasPrefix(filepath.Base(filepath.Dir(path)), workerReadyDirPrefix) {
		log.Debugf("ignoring unexpected worker readiness path %q", path)
		return
	}
	// #nosec G703 -- validated above to sit directly inside setup's private readiness dir.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		log.Debugf("report worker registration: %v", err)
	}
}
