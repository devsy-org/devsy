package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"
)

// BrowserTunnelParams bundles the arguments for browser-based IDE tunnels.
type BrowserTunnelParams struct {
	DevsyConfig      *config.Config
	Client           client2.BaseWorkspaceClient
	User             string
	TargetURL        string
	ForwardPorts     bool
	ExtraPorts       []string
	AuthSockID       string
	GitSSHSigningKey string

	// ExtraListeners holds pre-bound listeners for ExtraPorts entries,
	// keyed by host addr (e.g. "localhost:10800"). Set by the parent so the
	// helper can skip net.Listen and avoid a probe-to-listen TOCTOU race.
	ExtraListeners map[string]net.Listener

	// DaemonStartFunc is called when the client is a DaemonClient.
	// If nil, the SSH tunnel path is always used.
	DaemonStartFunc func(ctx context.Context) error

	// DisableIdleTimeout opts forwarded ports out of the
	// EXIT_AFTER_TIMEOUT idle shutdown. Set by callers whose lifecycle
	// is managed out-of-band (e.g. the detached browser-tunnel helper,
	// reaped by KillBrowserTunnel and devsy stop/delete).
	DisableIdleTimeout bool
}

// StartBrowserTunnel sets up a browser tunnel for IDE access, either via daemon or SSH.
func StartBrowserTunnel(ctx context.Context, p BrowserTunnelParams) error {
	if p.AuthSockID != "" {
		go func() {
			if err := SetupBackhaul(ctx, p.Client, p.AuthSockID); err != nil {
				log.Error("Failed to setup backhaul SSH connection: ", err)
			}
		}()
	}

	if p.DaemonStartFunc != nil {
		return p.DaemonStartFunc(ctx)
	}

	return startBrowserTunnelSSH(ctx, p)
}

func startBrowserTunnelSSH(ctx context.Context, p BrowserTunnelParams) error {
	return NewTunnel(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			writer := log.Writer(log.LevelDebug)
			defer func() { _ = writer.Close() }()

			// Stays root: runBrowserTunnelServices/RunServices reads
			// root-owned files (e.g. DevContainerResultPath) over this same
			// connection. GPG's shared /tmp lock/activity files are already
			// safe for a mixed root+remoteUser pair (see acquireGPGSetupLock,
			// ensureActivityFile) — this session doesn't need to match
			// remoteUser to avoid that collision.
			sshCmd, err := CreateSSHCommand(ctx, p.Client, "", []string{
				names.FlagValue(names.LogOutput, "raw"),
				names.FlagValue(names.ReuseSSHAuthSock, p.AuthSockID),
				names.Flag(names.Stdio),
			})
			if err != nil {
				return err
			}
			sshCmd.Stdout = stdout
			sshCmd.Stdin = stdin
			sshCmd.Stderr = writer
			return sshCmd.Run()
		},
		func(ctx context.Context, containerClient *ssh.Client) error {
			return runBrowserTunnelServices(ctx, p, containerClient)
		},
	)
}

func runBrowserTunnelServices(
	ctx context.Context,
	p BrowserTunnelParams,
	containerClient *ssh.Client,
) error {
	log.Infow("browser tunnel ready", "url", p.TargetURL, "done", "true")

	err := RunServices(
		ctx,
		RunServicesOptions{
			DevsyConfig:     p.DevsyConfig,
			ContainerClient: containerClient,
			User:            p.User,
			ForwardPorts:    p.ForwardPorts,
			ExtraPorts:      p.ExtraPorts,
			Workspace:       p.Client.WorkspaceConfig(),
			ConfigureDockerCredentials: p.DevsyConfig.ContextOption(
				config.ContextOptionSSHInjectDockerCredentials,
			) == config.BoolTrue,
			ConfigureGitCredentials: p.DevsyConfig.ContextOption(
				config.ContextOptionSSHInjectGitCredentials,
			) == config.BoolTrue,
			ConfigureGitSSHSignatureHelper: p.DevsyConfig.ContextOption(
				config.ContextOptionGitSSHSignatureForwarding,
			) == config.BoolTrue,
			GitSSHSigningKey:   p.GitSSHSigningKey,
			ExtraListeners:     p.ExtraListeners,
			DisableIdleTimeout: p.DisableIdleTimeout,
		},
	)
	if err != nil {
		return fmt.Errorf("run credentials server in browser tunnel: %w", err)
	}

	<-ctx.Done()
	return nil
}

// SetupBackhaul sets up a long-running SSH connection for backhaul.
func SetupBackhaul(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
	authSockID string,
) error {
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

	log.Info("Setting up backhaul SSH connection")

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	// 5 steps × 200ms ≈ 1s covers the workspace.json atomic-rename window
	// observed during a concurrent `agent workspace up` rewrite.
	backoff := wait.Backoff{
		Duration: 200 * time.Millisecond,
		Factor:   1.0,
		Steps:    5,
	}

	var lastErr error
	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(_ context.Context) (bool, error) {
		cmd := buildBackhaulCmd(ctx, backhaulCmdParams{
			execPath:   execPath,
			remoteUser: remoteUser,
			client:     client,
			authSockID: authSockID,
			writer:     writer,
		})
		lastErr = cmd.Run()
		if lastErr == nil {
			return true, nil
		}
		if !isTransientBackhaulErr(lastErr) {
			return false, lastErr
		}
		return false, nil
	})
	if err == nil {
		log.Infof("Done setting up backhaul")
		return nil
	}
	return interpretBackhaulResult(err, lastErr)
}

type backhaulCmdParams struct {
	execPath   string
	remoteUser string
	client     client2.BaseWorkspaceClient
	authSockID string
	writer     io.Writer
}

func buildBackhaulCmd(ctx context.Context, p backhaulCmdParams) *exec.Cmd {
	//nolint:gosec // execPath is the current binary, arguments are controlled
	cmd := exec.CommandContext(ctx,
		p.execPath,
		"workspace",
		"ssh",
		names.FlagTrue(names.AgentForwarding),
		names.FlagValue(names.ReuseSSHAuthSock, p.authSockID),
		names.FlagFalse(names.StartServices),
		names.Flag(names.User),
		p.remoteUser,
		names.Flag(names.Context),
		p.client.Context(),
		p.client.Workspace(),
		names.FlagValue(names.LogOutput, "raw"),
		names.Flag(names.Command),
		"while true; do sleep 6000000; done", // sleep infinity is not available on all systems
	)
	if log.DebugEnabled() {
		cmd.Args = append(cmd.Args, names.Flag(names.Debug))
	}
	cmd.Stdout = p.writer
	cmd.Stderr = p.writer
	return cmd
}

func interpretBackhaulResult(err, lastErr error) error {
	if wait.Interrupted(err) {
		// Either retries exhausted or ctx cancelled; surface the underlying
		// subprocess error if one is available, else the wait error.
		if lastErr != nil && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			return lastErr
		}
	}
	return err
}

// isTransientBackhaulErr reports whether the `devsy ssh` subprocess exited with
// a retryable status (e.g. a workspace-registration race).
func isTransientBackhaulErr(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == exitcode.Retryable
}

// CreateSSHCommand builds an exec.Cmd that runs `devsy ssh` with the given arguments.
// user must match the ssh-server/gpg-setup sessions' user, or they collide on
// shared /tmp coordination files (devsy-gpg-setup.lock, devsy.activity).
func CreateSSHCommand(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
	user string,
	extraArgs []string,
) (*exec.Cmd, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := buildSSHCommandArgs(sshCommandArgsParams{
		clientContext: client.Context(),
		workspace:     client.Workspace(),
		user:          user,
		debug:         log.DebugEnabled(),
		extraArgs:     extraArgs,
	})

	//nolint:gosec // execPath is the current binary, arguments are controlled
	return exec.CommandContext(ctx, execPath, args...), nil
}

// sshCommandArgsParams bundles buildSSHCommandArgs' inputs so the function
// takes one argument instead of five.
type sshCommandArgsParams struct {
	clientContext string
	workspace     string
	user          string
	debug         bool
	extraArgs     []string
}

// buildSSHCommandArgs constructs the argument list for `devsy ssh`.
func buildSSHCommandArgs(p sshCommandArgsParams) []string {
	user := p.user
	if user == "" {
		user = "root"
	}
	args := []string{
		"workspace",
		"ssh",
		names.FlagValue(names.User, user),
		names.FlagFalse(names.AgentForwarding),
		names.FlagFalse(names.StartServices),
		names.Flag(names.Context),
		p.clientContext,
		p.workspace,
	}
	if p.debug {
		args = append(args, names.Flag(names.Debug))
	}
	args = append(args, p.extraArgs...)
	return args
}
