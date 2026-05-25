package helper

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	pkglog "github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/tunnel"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// BrowserTunnelCmd holds the browser-tunnel helper flags.
type BrowserTunnelCmd struct {
	*flags.GlobalFlags

	Workspace        string
	TargetURL        string
	AuthSockID       string
	ForwardPorts     bool
	ExtraPorts       []string
	User             string
	GitSSHSigningKey string
	InheritListeners []string
}

// NewBrowserTunnelCmd creates a new browser-tunnel helper command.
func NewBrowserTunnelCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &BrowserTunnelCmd{GlobalFlags: globalFlags}
	c := &cobra.Command{
		Use:    "browser-tunnel",
		Short:  "Runs a long-lived browser IDE tunnel in the background",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	c.Flags().StringVar(&cmd.Workspace, "workspace", "", "Workspace name or ID")
	c.Flags().StringVar(&cmd.TargetURL, "target-url", "", "Target URL for the browser IDE")
	c.Flags().StringVar(&cmd.AuthSockID, "auth-sock-id", "", "Reused SSH_AUTH_SOCK id")
	c.Flags().BoolVar(&cmd.ForwardPorts, "forward-ports", false, "Whether to forward ports")
	c.Flags().StringArrayVar(&cmd.ExtraPorts, "extra-ports", nil, "Extra ports to forward")
	c.Flags().StringVar(&cmd.User, "user", "", "Remote user")
	c.Flags().
		StringVar(&cmd.GitSSHSigningKey, "git-ssh-signing-key", "", "Git SSH signing key")
	c.Flags().StringArrayVar(
		&cmd.InheritListeners,
		"inherit-listener",
		nil,
		"Inherited listener fd, format host:port=fd (repeatable, unix only)",
	)
	return c
}

// parseInheritedListeners converts --inherit-listener entries into a map of
// host:port to net.Listener by wrapping the inherited file descriptors.
func parseInheritedListeners(entries []string) (map[string]net.Listener, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]net.Listener, len(entries))
	for _, entry := range entries {
		eqIdx := strings.LastIndex(entry, "=")
		if eqIdx < 0 {
			return nil, fmt.Errorf("invalid --inherit-listener %q (expected host:port=fd)", entry)
		}
		hostAddr := entry[:eqIdx]
		fdStr := entry[eqIdx+1:]
		fd, err := strconv.Atoi(fdStr)
		if err != nil {
			return nil, fmt.Errorf("invalid fd in --inherit-listener %q: %w", entry, err)
		}
		if fd < 0 {
			return nil, fmt.Errorf("invalid negative fd %d in --inherit-listener %q", fd, entry)
		}
		//nolint:gosec // fd is bounds-checked above
		f := os.NewFile(uintptr(fd), "inherited-listener-"+hostAddr)
		if f == nil {
			return nil, fmt.Errorf("invalid fd %d for %s", fd, hostAddr)
		}
		l, err := net.FileListener(f)
		// FileListener dup'd the fd; close ours regardless of success.
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("wrap inherited listener for %s: %w", hostAddr, err)
		}
		out[hostAddr] = l
	}
	return out, nil
}

// Run runs the browser-tunnel helper command.
func (cmd *BrowserTunnelCmd) Run(ctx context.Context) error {
	if cmd.Workspace == "" {
		return fmt.Errorf("workspace is required")
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return fmt.Errorf("load devsy config: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopSignals := installSignalCancel(ctx, cancel)
	defer stopSignals()

	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{cmd.Workspace},
		Owner:       cmd.Owner,
	})
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	// Best-effort cleanup of the tunnel state file when this child exits.
	// Only remove if we still own it (PID matches) so a re-spawned tunnel's
	// state isn't accidentally clobbered.
	defer cleanupOwnedTunnelState(client.Context(), client.Workspace())

	extraListeners, err := parseInheritedListeners(cmd.InheritListeners)
	if err != nil {
		return fmt.Errorf("parse inherited listeners: %w", err)
	}

	return tunnel.StartBrowserTunnel(tunnel.BrowserTunnelParams{
		Ctx:              ctx,
		DevsyConfig:      devsyConfig,
		Client:           client,
		User:             cmd.User,
		TargetURL:        cmd.TargetURL,
		ForwardPorts:     cmd.ForwardPorts,
		ExtraPorts:       cmd.ExtraPorts,
		AuthSockID:       cmd.AuthSockID,
		GitSSHSigningKey: cmd.GitSSHSigningKey,
		ExtraListeners:   extraListeners,
	})
}

// installSignalCancel registers a signal handler that cancels ctx on the
// usual termination signals. The returned func should be deferred to stop
// signal delivery.
func installSignalCancel(ctx context.Context, cancel context.CancelFunc) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()
	return func() { signal.Stop(signals) }
}

// cleanupOwnedTunnelState removes the tunnel state file for the workspace
// only if it still records this process's PID, so a re-spawned tunnel's
// state isn't accidentally clobbered.
func cleanupOwnedTunnelState(contextName, workspaceID string) {
	state, rerr := opener.ReadTunnelState(contextName, workspaceID)
	if rerr != nil || state == nil || state.PID != os.Getpid() {
		return
	}
	statePath, perr := opener.TunnelStateFilePath(contextName, workspaceID)
	if perr != nil {
		return
	}
	if rmErr := os.Remove(statePath); rmErr != nil && !os.IsNotExist(rmErr) {
		pkglog.Debugf("remove tunnel state file: %v", rmErr)
	}
}
