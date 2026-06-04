package up

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/spf13/cobra"
)

// UpCmd holds the up cmd flags.
type UpCmd struct {
	provider2.CLIOptions
	*flags.GlobalFlags

	Machine string

	ProviderOptions []string

	ConfigureSSH       bool
	GPGAgentForwarding bool
	SSHTunnelMode      bool
	IDELaunch          opener.IDELaunchMode
	Reconfigure        bool

	SSHConfigPath      string
	SecretsFile        string
	FeatureSecretsFile string
	WorkspaceFolder    string

	DotfilesSource        string
	DotfilesScript        string
	DotfilesTargetPath    string
	DotfilesScriptEnv     []string // Key=Value to pass to install script
	DotfilesScriptEnvFile []string // Paths to files containing Key=Value pairs to pass to install script
}

// Options is the structured input form of the up command, for non-CLI callers.
type Options struct {
	Source           string // git URL, local path, image, or workspace name
	Name             string // explicit workspace ID override
	Provider         string // provider name override
	IDE              string // ide name; "none" to skip launching
	DevcontainerPath string // path to devcontainer.json, relative to project
}

// RunFromOptions runs the up command's logic without going through cobra.
// This is the same code path execute() follows, exposed for callers (such as
// the MCP server) that have structured input instead of CLI args.
// NOTE: WithSignals is intentionally skipped — the caller controls cancellation
// via ctx.
func RunFromOptions(ctx context.Context, g *flags.GlobalFlags, opts Options) error {
	cmd := buildUpCmd(g, opts)
	if err := cmd.validate(); err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return fmt.Errorf("load devsy config: %w", err)
	}
	cmd.applyConfig(devsyConfig)

	args := []string{opts.Source}
	client, err := cmd.prepareClient(ctx, devsyConfig, args)
	if err != nil {
		return fmt.Errorf("prepare workspace client: %w", err)
	}
	if cmd.ExtraDevContainerPath != "" && client.Provider() != "docker" {
		return fmt.Errorf("extra devcontainer file is only supported with local provider")
	}
	telemetry.CollectorCLI.SetClient(client)
	return cmd.Run(ctx, devsyConfig, client, args)
}

// buildUpCmd constructs an UpCmd from structured options for non-CLI callers.
func buildUpCmd(g *flags.GlobalFlags, opts Options) *UpCmd {
	ide := opts.IDE
	if ide == "" {
		ide = "none"
	}
	cmd := &UpCmd{GlobalFlags: g}
	cmd.IDE = ide
	cmd.DevContainerPath = opts.DevcontainerPath
	if opts.Name != "" {
		cmd.ID = opts.Name
	}
	return cmd
}

// NewUpCmd creates a new up command.
func NewUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{GlobalFlags: f}
	upCmd := &cobra.Command{
		Use:   "up [flags] [workspace-path|workspace-name]",
		Short: "Starts a new workspace",
		RunE:  cmd.execute,
	}
	cmd.registerFlags(upCmd)
	return upCmd
}

// Run runs the command logic.
func (cmd *UpCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	args []string,
) error {
	cmd.prepareWorkspace(client)

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	emitJSON := mode == output.ModeJSON

	wctx, err := cmd.executeDevsyUp(ctx, devsyConfig, client)
	if err != nil {
		return reportErr(err, emitJSON)
	}
	if wctx == nil || cmd.Prebuild {
		return nil // Platform mode or prebuild-only run.
	}
	return cmd.finalizeUp(ctx, &finalizeUpArgs{
		devsyConfig: devsyConfig,
		client:      client,
		wctx:        wctx,
		emitJSON:    emitJSON,
	})
}

// applyConfig sets config-derived fields after loading the devsy config.
// Used by both execute() and RunFromOptions().
func (cmd *UpCmd) applyConfig(devsyConfig *config.Config) {
	if devsyConfig.ContextOption(config.ContextOptionSSHStrictHostKeyChecking) == config.BoolTrue {
		cmd.StrictHostKeyChecking = true
	}
	cmd.resolveDotfilesOptions(devsyConfig)
}

type finalizeUpArgs struct {
	devsyConfig *config.Config
	client      client2.BaseWorkspaceClient
	wctx        *workspaceContext
	emitJSON    bool
}

// finalizeUp performs the post-up steps: workspace configuration, optional SSH
// tunnel, IDE launch, and JSON envelope emission. Split out to keep Run small.
func (cmd *UpCmd) finalizeUp(ctx context.Context, args *finalizeUpArgs) error {
	if err := cmd.configureWorkspace(args.devsyConfig, args.client, args.wctx); err != nil {
		return reportErr(err, args.emitJSON)
	}

	if cleanup := cmd.maybeStartTunnel(
		ctx,
		args.devsyConfig,
		args.client,
		args.wctx,
	); cleanup != nil {
		defer cleanup()
	}
	if err := cmd.reconfigureSSHWithTunnel(args.devsyConfig, args.client, args.wctx); err != nil {
		log.Warnf("Failed to reconfigure SSH with tunnel port: %v", err)
	}

	ideURL, err := cmd.openIDE(ctx, args.devsyConfig, args.client, args.wctx)
	if err != nil {
		return reportErr(err, args.emitJSON)
	}
	if args.emitJSON {
		emitUpResult(args.wctx, ideURL)
	}
	if args.wctx.tunnelPort > 0 {
		log.Infof(
			"SSH tunnel active on port %d, waiting for shutdown signal",
			args.wctx.tunnelPort,
		)
		<-ctx.Done()
	}
	return nil
}

// maybeStartTunnel starts the SSH tunnel when enabled and returns its cleanup
// func, or nil if no tunnel is active. Failures are logged and demoted to a
// fallback ProxyCommand path.
func (cmd *UpCmd) maybeStartTunnel(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	wctx *workspaceContext,
) func() {
	if !cmd.SSHTunnelMode &&
		devsyConfig.ContextOption(config.ContextOptionSSHTunnelMode) != config.BoolTrue {
		return nil
	}
	tunnelPort, tunnelCleanup, err := cmd.startTunnel(ctx, devsyConfig, client, wctx)
	if err != nil {
		log.Warnf("Failed to start SSH tunnel, falling back to ProxyCommand: %v", err)
		return nil
	}
	wctx.tunnelPort = tunnelPort
	return tunnelCleanup
}

// reportErr writes the error to JSON output when requested and returns it for the caller.
func reportErr(err error, emitJSON bool) error {
	if emitJSON {
		_ = config2.WriteErrorJSON(os.Stdout, err.Error())
	}
	return err
}

// emitUpResult writes the JSON result envelope for a completed `up` invocation.
func emitUpResult(wctx *workspaceContext, ideURL string) {
	containerID := ""
	var warnings []string
	if wctx.result != nil {
		if wctx.result.ContainerDetails != nil {
			containerID = wctx.result.ContainerDetails.ID
		}
		warnings = wctx.result.HostWarnings
	}
	_ = config2.WriteResultJSON(os.Stdout, config2.ResultEnvelope{
		ContainerID:           containerID,
		RemoteUser:            wctx.user,
		RemoteWorkspaceFolder: wctx.workdir,
		URL:                   ideURL,
		Warnings:              warnings,
	})
}

func (cmd *UpCmd) execute(cobraCmd *cobra.Command, args []string) error {
	if err := cmd.validate(); err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return fmt.Errorf("load devsy config: %w", err)
	}
	cmd.applyConfig(devsyConfig)

	ctx, cancel := WithSignals(cobraCmd.Context())
	defer cancel()

	client, err := cmd.prepareClient(ctx, devsyConfig, args)
	if err != nil {
		return fmt.Errorf("prepare workspace client: %w", err)
	}
	if cmd.ExtraDevContainerPath != "" && client.Provider() != "docker" {
		return fmt.Errorf("extra devcontainer file is only supported with local provider")
	}

	telemetry.CollectorCLI.SetClient(client)
	return cmd.Run(ctx, devsyConfig, client, args)
}

// workspaceContext holds the result of workspace preparation.
type workspaceContext struct {
	result     *config2.Result
	user       string
	workdir    string
	tunnelPort int
}

// resolveDotfilesOptions populates DotfilesRepo and DotfilesScript
// from the CLI flags and config context options so they flow to the container.
func (cmd *UpCmd) resolveDotfilesOptions(devsyConfig *config.Config) {
	repo := devsyConfig.ContextOption(config.ContextOptionDotfilesURL)
	if cmd.DotfilesSource != "" {
		repo = cmd.DotfilesSource
	}
	cmd.DotfilesRepo = repo

	script := devsyConfig.ContextOption(config.ContextOptionDotfilesScript)
	if cmd.DotfilesScript != "" {
		script = cmd.DotfilesScript
	}
	cmd.CLIOptions.DotfilesScript = script

	if cmd.DotfilesTargetPath != "" {
		cmd.CLIOptions.DotfilesTargetPath = cmd.DotfilesTargetPath
	}
}

// prepareWorkspace handles initial setup and validation.
func (cmd *UpCmd) prepareWorkspace(client client2.BaseWorkspaceClient) {
	if cmd.Reset {
		cmd.Recreate = true
	}

	if cmd.Recreate {
		// Kill any existing detached browser tunnel before recreating the
		// container so the new tunnel can't race with a now-broken one.
		opener.KillBrowserTunnel(client.Context(), client.Workspace())
	}

	targetIDE := client.WorkspaceConfig().IDE.Name
	if cmd.IDE != "" {
		targetIDE = cmd.IDE
	}

	if !cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		cmd.SSHAuthSockID = util.RandStringBytes(10)
		log.Debug("Reusing SSH_AUTH_SOCK", cmd.SSHAuthSockID)
	} else if cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		log.Debug(
			"Reusing SSH_AUTH_SOCK is not supported with platform mode, consider launching the IDE from the platform UI",
		)
	}
}

// executeDevsyUp runs the agent and returns workspace context.
func (cmd *UpCmd) executeDevsyUp(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*workspaceContext, error) {
	result, err := cmd.devsyUp(ctx, devsyConfig, client)
	// Prefer the structured error message forwarded from the agent over
	// the generic SSH-level wrapper, so callers see the actual cause
	// (e.g. host requirements not met) instead of a generic fallback.
	if result != nil && result.Error != "" {
		if err != nil {
			return nil, fmt.Errorf("start workspace: %s: %w", result.Error, err)
		}
		return nil, fmt.Errorf("start workspace: %s", result.Error)
	}
	if err != nil {
		return nil, fmt.Errorf("start workspace: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("did not receive a result back from agent")
	}
	if cmd.Platform.Enabled {
		return nil, nil
	}
	// Guard against a result that lacks the substitution context — that
	// indicates the agent returned a half-populated result (e.g. an inner
	// container-setup failure that didn't carry through as result.Error).
	// Without this, downstream openIDE would nil-deref on
	// SubstitutionContext.ContainerWorkspaceFolder.
	if result.SubstitutionContext == nil {
		return nil, fmt.Errorf(
			"agent returned an incomplete result (missing substitution context); " +
				"check earlier logs for the underlying setup failure",
		)
	}

	user := config2.GetRemoteUser(result)
	workdir := ""
	if result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		workdir = result.MergedConfig.WorkspaceFolder
	}
	if client.WorkspaceConfig().Source.GitSubPath != "" {
		result.SubstitutionContext.ContainerWorkspaceFolder = filepath.Join(
			result.SubstitutionContext.ContainerWorkspaceFolder,
			client.WorkspaceConfig().Source.GitSubPath,
		)
		workdir = result.SubstitutionContext.ContainerWorkspaceFolder
	}
	if cmd.WorkspaceFolder != "" {
		result.SubstitutionContext.ContainerWorkspaceFolder = cmd.WorkspaceFolder
		workdir = cmd.WorkspaceFolder
	}

	return &workspaceContext{result: result, user: user, workdir: workdir}, nil
}

func WithSignals(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT)

	// done is closed by the returned cleanup so both goroutines exit when the
	// caller finishes — signal.Stop alone is not enough because a goroutine
	// already blocked on <-signals will never unblock once Stop is called.
	done := make(chan struct{})

	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		case <-done:
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		select {
		case <-signals:
			// force shutdown if context is done and another signal arrives
			os.Exit(1)
		case <-done:
		}
	}()

	return ctx, func() {
		cancel()
		signal.Stop(signals)
		close(done)
	}
}
