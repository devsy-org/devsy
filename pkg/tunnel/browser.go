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

			sshCmd, err := CreateSSHCommand(ctx, p.Client, []string{
				"--log-output=raw",
				fmt.Sprintf("--reuse-ssh-auth-sock=%s", p.AuthSockID),
				"--stdio",
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
			GitSSHSigningKey: p.GitSSHSigningKey,
			ExtraListeners:   p.ExtraListeners,
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

	buildCmd := func() *exec.Cmd {
		//nolint:gosec // execPath is the current binary, arguments are controlled
		cmd := exec.CommandContext(ctx,
			execPath,
			"ssh",
			"--agent-forwarding=true",
			fmt.Sprintf("--reuse-ssh-auth-sock=%s", authSockID),
			"--start-services=false",
			"--user",
			remoteUser,
			"--context",
			client.Context(),
			client.Workspace(),
			"--log-output=raw",
			"--command",
			"while true; do sleep 6000000; done", // sleep infinity is not available on all systems
		)
		if log.DebugEnabled() {
			cmd.Args = append(cmd.Args, "--debug")
		}
		cmd.Stdout = writer
		cmd.Stderr = writer
		return cmd
	}

	// 5 steps × 200ms ≈ 1s covers the workspace.json atomic-rename window
	// observed during a concurrent `agent workspace up` rewrite.
	backoff := wait.Backoff{
		Duration: 200 * time.Millisecond,
		Factor:   1.0,
		Steps:    5,
	}

	var lastErr error
	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(_ context.Context) (bool, error) {
		cmd := buildCmd()
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
	if wait.Interrupted(err) {
		// Either retries exhausted or ctx cancelled; surface the underlying
		// subprocess error if we have one, else the wait error.
		if lastErr != nil && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			return lastErr
		}
	}
	return err
}

// isTransientBackhaulErr returns true when the `devsy ssh` subprocess exited
// with exitcode.WorkspaceNotFound, indicating the workspace registration has
// not yet propagated (a race with concurrent workspace.json writers). The
// exit-code contract is set in cmd/root.go and the constant lives in
// pkg/exitcode; using it here keeps this decision typed rather than relying
// on stderr substring matching.
func isTransientBackhaulErr(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == exitcode.WorkspaceNotFound
}

// CreateSSHCommand builds an exec.Cmd that runs `devsy ssh` with the given arguments.
func CreateSSHCommand(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
	extraArgs []string,
) (*exec.Cmd, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := buildSSHCommandArgs(
		client.Context(),
		client.Workspace(),
		log.DebugEnabled(),
		extraArgs,
	)

	//nolint:gosec // execPath is the current binary, arguments are controlled
	return exec.CommandContext(ctx, execPath, args...), nil
}

// buildSSHCommandArgs constructs the argument list for `devsy ssh`.
func buildSSHCommandArgs(clientContext, workspace string, debug bool, extraArgs []string) []string {
	args := []string{
		"ssh",
		"--user=root",
		"--agent-forwarding=false",
		"--start-services=false",
		"--context",
		clientContext,
		workspace,
	}
	if debug {
		args = append(args, "--debug")
	}
	args = append(args, extraArgs...)
	return args
}
