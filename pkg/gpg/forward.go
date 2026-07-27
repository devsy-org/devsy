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

var (
	forwardRestartMinBackoff = time.Second
	forwardRestartMaxBackoff = 30 * time.Second
)

// ForwardAgent starts a supervised background SSH connection that forwards the
// local GPG agent, restarting it until ctx is cancelled.
func ForwardAgent(ctx context.Context, client client2.BaseWorkspaceClient) error {
	log.Debug("gpg forwarding enabled, performing immediately")

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

	go superviseForward(ctx, execPath, args)

	return nil
}

func superviseForward(ctx context.Context, execPath string, args []string) {
	backoff := forwardRestartMinBackoff
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

		if time.Since(start) >= forwardRestartMaxBackoff {
			backoff = forwardRestartMinBackoff
		}
		log.Errorf("gpg-agent forward exited (%v); restarting in %s", runErr, backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, forwardRestartMaxBackoff)
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
