package gpg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
)

type backoff struct {
	min, max time.Duration
}

var forwardRestartBackoff = backoff{min: time.Second, max: 30 * time.Second}

const EnvForwardReadyFD = "DEVSY_GPG_FORWARD_READY_FD"

// forwardReadyTimeout bounds ForwardAgent's wait so a stuck forward cannot
// block the caller indefinitely.
const forwardReadyTimeout = 30 * time.Second

// ForwardAgent starts a supervised background SSH connection that forwards
// the local GPG agent, restarting it until ctx is cancelled. It waits for the
// first attempt's tunnel setup to finish so callers that immediately hand
// control to the workspace (e.g. opening a browser IDE terminal) do not race
// an agent that is not forwarded yet.
func ForwardAgent(ctx context.Context, client client2.BaseWorkspaceClient) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	remoteUser, err := devssh.GetUser(
		client.WorkspaceConfig().ID,
		client.WorkspaceConfig().SSHConfigPath,
		client.WorkspaceConfig().SSHConfigIncludePath,
	)
	if err != nil {
		remoteUser = "root"
	}

	log.Info("forwarding gpg-agent")

	args := buildForwardArgs(remoteUser, client.Context(), client.Workspace())

	spec := forwardSpec{execPath: execPath, args: args, backoff: forwardRestartBackoff}
	ready := make(chan struct{}, 1)
	go superviseForward(ctx, spec, ready)

	select {
	case <-ready:
	case <-time.After(forwardReadyTimeout):
		log.Warnf(
			"timed out waiting for gpg-agent forward to become ready; continuing without waiting",
		)
	case <-ctx.Done():
	}

	return nil
}

type forwardSpec struct {
	execPath string
	args     []string
	backoff  backoff
}

func superviseForward(ctx context.Context, spec forwardSpec, ready chan<- struct{}) {
	delay := spec.backoff.min
	pendingReady := ready
	for {
		if ctx.Err() != nil {
			return
		}

		start := time.Now()
		reported, runErr := runForwardOnce(ctx, spec.execPath, spec.args, pendingReady)
		if reported {
			pendingReady = nil // readiness has been reported; no need to keep trying
		}
		if ctx.Err() != nil {
			return
		}

		if time.Since(start) >= spec.backoff.max {
			delay = spec.backoff.min
		}
		log.Errorf("gpg-agent forward exited (%v); restarting in %s", runErr, delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, spec.backoff.max)
	}
}

// runForwardOnce runs a single attempt of the forwarding child. If ready is
// non-nil, it reports whether the child actually signaled readiness via
// EnvForwardReadyFD (a written byte), so callers can keep offering the
// readiness pipe to later attempts when this one merely failed to set up or
// exited without ever reporting.
func runForwardOnce(
	ctx context.Context,
	execPath string,
	args []string,
	ready chan<- struct{},
) (reported bool, err error) {
	//nolint:gosec // execPath comes from os.Executable()
	cmd := exec.CommandContext(ctx, execPath, args...)

	if ready == nil {
		return false, cmd.Run()
	}

	r, w, err := os.Pipe()
	if err != nil {
		return false, cmd.Run()
	}
	cmd.ExtraFiles = []*os.File{w}
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=3", EnvForwardReadyFD))

	if err := cmd.Start(); err != nil {
		_ = w.Close()
		_ = r.Close()
		return false, err
	}
	_ = w.Close() // the child holds the only remaining write end

	signaled := make(chan bool, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		_ = r.Close()
		ok := n > 0
		if ok {
			signalOnce(ready)
		}
		signaled <- ok
	}()

	err = cmd.Wait()
	return <-signaled, err
}

func signalOnce(ready chan<- struct{}) {
	select {
	case ready <- struct{}{}:
	default:
	}
}

func buildForwardArgs(user, context, workspace string) []string {
	return []string{
		"workspace",
		"ssh",
		names.FlagTrue(names.SSHGPGForwarding),
		names.FlagTrue(names.AgentForwarding),
		names.FlagTrue(names.StartServices),
		names.Flag(names.User),
		user,
		names.Flag(names.Context),
		context,
		workspace,
		names.FlagValue(names.LogOutput, "raw"),
		names.Flag(names.Command), "sleep infinity",
	}
}
