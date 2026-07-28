package gpg

import (
	"context"
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

// ForwardAgent starts a supervised background SSH connection that forwards the
// local GPG agent, restarting it until ctx is cancelled.
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

	go superviseForward(ctx, execPath, args, forwardRestartBackoff)

	return nil
}

func superviseForward(ctx context.Context, execPath string, args []string, b backoff) {
	delay := b.min
	for {
		if ctx.Err() != nil {
			return
		}

		start := time.Now()
		//nolint:gosec // execPath comes from os.Executable()
		runErr := exec.CommandContext(ctx, execPath, args...).Run()
		if ctx.Err() != nil {
			return
		}

		if time.Since(start) >= b.max {
			delay = b.min
		}
		log.Errorf("gpg-agent forward exited (%v); restarting in %s", runErr, delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, b.max)
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
