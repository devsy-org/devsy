package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"golang.org/x/crypto/ssh"
)

// NewWorkspaceRuntime keeps agent-side runtimes on the workspace's transport,
// including when the provider agent runs on a different machine.
func NewWorkspaceRuntime(
	cfg *config.Config,
	workspaceClient client.BaseWorkspaceClient,
	override string,
) (ContainerRuntime, error) {
	full, ok := workspaceClient.(client.WorkspaceClient)
	if ok {
		_, info, err := full.AgentInfo(provider.CLIOptions{})
		if err != nil {
			return nil, err
		}
		switch info.Agent.Driver {
		case provider.ExternalDriver, provider.MicrosandboxDriver:
			if override != "" {
				return nil, errors.New("--docker-path cannot override an agent-side runtime")
			}
			return &tunnelRuntime{client: full, config: cfg}, nil
		}
	}
	return NewContainerRuntime(workspaceClient.WorkspaceConfig(), override)
}

type tunnelRuntime struct {
	client client.WorkspaceClient
	config *config.Config
}

func (r *tunnelRuntime) FindRunning(
	ctx context.Context, workspaceID string, idLabels []string,
) (*devcconfig.ContainerDetails, error) {
	if workspaceID != devcontainer.GetRunnerIDFromWorkspace(r.client.WorkspaceConfig()) {
		return nil, errors.New("exec target does not match the workspace")
	}
	state, err := r.client.Status(ctx, client.StatusOptions{ContainerStatus: true})
	if err != nil {
		return nil, err
	}
	if state != client.StatusRunning {
		return nil, fmt.Errorf(
			"workspace %q is not running (status: %s)",
			r.client.Workspace(),
			state,
		)
	}
	workspace := r.client.WorkspaceConfig()
	result, err := provider.LoadWorkspaceResult(workspace.Context, workspace.ID)
	if err != nil {
		return nil, err
	}
	return validateTunnelExecResult(result, idLabels)
}

func validateTunnelExecResult(
	result *devcconfig.Result,
	idLabels []string,
) (*devcconfig.ContainerDetails, error) {
	if result == nil || result.ContainerDetails == nil || result.ContainerDetails.ID == "" {
		return nil, errors.New("workspace has no saved container result; run workspace up first")
	}
	for _, label := range idLabels {
		key, value, _ := strings.Cut(label, "=")
		if actual, found := result.ContainerDetails.Config.Labels[key]; !found || actual != value {
			return nil, fmt.Errorf("workspace container does not match label %q", key)
		}
	}
	return result.ContainerDetails, nil
}

func (r *tunnelRuntime) Exec(ctx context.Context, req ExecRequest) (int, error) {
	command, err := tunnelExecCommand(req)
	if err != nil {
		return -1, err
	}
	sshUser := ""
	if tunnelExecCredentialIdentity(req.Target.User) {
		sshUser = req.Target.User
	}
	err = tunnel.NewContainerTunnel(r.client).AsUser(sshUser).
		Run(ctx, func(ctx context.Context, conn *ssh.Client) error {
			return runTunnelExec(ctx, conn, req, command)
		}, r.config, nil)
	if err == nil {
		return 0, nil
	}
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if exit, ok := errors.AsType[*ssh.ExitError](err); ok {
		return exit.ExitStatus(), nil
	}
	return -1, err
}

func tunnelExecCommand(req ExecRequest) (string, error) {
	if len(req.Argv) == 0 || req.Argv[0] == "" {
		return "", errors.New("exec requires a nonempty executable")
	}
	args := []string{"env", "--"}
	keys := make([]string, 0, len(req.Env))
	for key := range req.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, key+"="+req.Env[key])
	}
	args = append(args, req.Argv...)
	command := "exec " + shellescape.QuoteCommand(args)
	if req.Workdir != "" {
		command = "cd -- " + shellescape.Quote(req.Workdir) + " && " + command
	}
	return wrapTunnelExecUser(req.Target.User, command), nil
}

func wrapTunnelExecUser(user, command string) string {
	if user == "" || user == "root" || tunnelExecCredentialIdentity(user) {
		return command
	}
	return "exec " + shellescape.QuoteCommand(
		[]string{"su", "-s", "/bin/sh", "-c", command, "--", user},
	)
}

func tunnelExecCredentialIdentity(user string) bool {
	return strings.Contains(user, ":") || (user != "" && strings.Trim(user, "0123456789") == "")
}

func (r *tunnelRuntime) ProbeEnv(
	ctx context.Context,
	target ContainerTarget,
	probe string,
) map[string]string {
	return probeEnvWithCommand(ctx, probe, func(flag, command string, stdout io.Writer) error {
		code, err := r.Exec(ctx, ExecRequest{
			Target: target, Argv: []string{"sh", flag, command}, Stdout: stdout, Stderr: io.Discard,
		})
		if err == nil && code != 0 {
			return fmt.Errorf("environment probe exited with code %d", code)
		}
		return err
	})
}

// Guest statuses, including 129/130/143, are command outcomes rather than
// evidence that the local transport was canceled. Never echo the command here:
// it contains values from remoteEnv and --remote-env.
func runTunnelExecSession(
	ctx context.Context,
	session *ssh.Session,
	req ExecRequest,
	command string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() {
		_ = session.Signal(ssh.SIGINT)
		_ = session.Close()
	})
	defer stop()
	session.Stdin, session.Stdout, session.Stderr = req.Stdin, req.Stdout, req.Stderr
	return session.Run(command)
}

func runTunnelExec(ctx context.Context, conn *ssh.Client, req ExecRequest, command string) error {
	session, err := conn.NewSession()
	if err != nil {
		return err
	}
	var restore func()
	defer func() {
		_ = session.Close()
		if restore != nil {
			restore()
		}
	}()
	if req.TTY {
		restore, err = requestExecPTY(ctx, session, req.Stdin)
		if err != nil {
			return err
		}
	}
	return runTunnelExecSession(ctx, session, req, command)
}
