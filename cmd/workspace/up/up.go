package up

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UpCmd holds the up cmd flags.
type UpCmd struct {
	provider2.CLIOptions
	*flags.GlobalFlags

	Machine string

	ProviderOptions []string

	FromSnapshot string // snapshot ref to restore from, e.g. ghcr.io/acme/s:my-ws-20260731150405-abcxyz

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

	// Read via Changed() so unset is distinguishable from explicit false.
	pullFromInsideContainerFlag bool
	// See cmd.runDetached.
	Detach bool
	// Set only on the detached child re-exec.
	taskID string
	// Out receives result/error JSON envelopes; nil falls back to os.Stdout.
	Out io.Writer
	// Set at the start of Run; nil until then.
	statusReporter status.Reporter
}

// Options is the structured input form of the up command.
type Options struct {
	Source           string // git URL, local path, image, or workspace name
	Name             string // explicit workspace ID override
	Provider         string // provider name override
	IDE              string // ide name; "none" to skip launching
	DevcontainerPath string // path to devcontainer.json, relative to project
	// DevContainerSource overrides devcontainer discovery (e.g. "image:<ref>"),
	// used by snapshot restore to run the committed image directly.
	DevContainerSource string
	// RunArgs are extra `docker run` arguments to apply when DevContainerSource
	// suppresses the project devcontainer.json, so runArgs it would have set
	// (e.g. --add-host) still take effect. Used by snapshot restore to replay
	// the original devcontainer.json's runArgs.
	RunArgs []string
	// ContainerEnv are extra container environment variables to apply under
	// the same suppressed-discovery circumstances as RunArgs. Used by
	// snapshot restore to replay the original devcontainer.json's containerEnv.
	ContainerEnv map[string]string
	// RemoteUser is the remoteUser to replay under the same
	// suppressed-discovery circumstances as RunArgs. Used by snapshot restore.
	RemoteUser string
}

type HeadlessOptions struct {
	GlobalFlags        *flags.GlobalFlags
	DevsyConfig        *config.Config
	CLIOptions         provider2.CLIOptions
	ProviderOptions    []string
	SecretsFile        string
	FeatureSecretsFile string
}

func RunHeadless(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
	opts HeadlessOptions,
) (*config2.Result, error) {
	gCopy := *opts.GlobalFlags
	cmd := &UpCmd{
		GlobalFlags:        &gCopy,
		CLIOptions:         opts.CLIOptions,
		ProviderOptions:    opts.ProviderOptions,
		SecretsFile:        opts.SecretsFile,
		FeatureSecretsFile: opts.FeatureSecretsFile,
		ConfigureSSH:       false,
		IDELaunch:          opener.LaunchSkip,
		Out:                io.Discard,
	}
	cmd.IDE = string(config.IDENone)
	mountGitRootDefault := true
	cmd.MountWorkspaceGitRoot = &mountGitRootDefault

	if err := cmd.validate(); err != nil {
		return nil, err
	}
	var (
		project *projectSecretContext
		err     error
	)
	if cfg := client.WorkspaceConfig(); cfg != nil {
		project, err = cmd.discoverProjectSecrets(ctx, &cfg.Source)
		if err != nil {
			return nil, err
		}
	}
	if err := cmd.prepareSecretsWithProject(ctx, opts.DevsyConfig, project); err != nil {
		return nil, err
	}
	cmd.prepareWorkspace(client)

	wctx, err := cmd.executeDevsyUp(ctx, opts.DevsyConfig, client)
	if err != nil {
		return nil, err
	}
	if wctx == nil {
		return nil, fmt.Errorf("up returned no workspace context")
	}
	return wctx.result, nil
}

// RunFromOptions runs the up logic without cobra. Callers own ctx cancellation;
// WithSignals is intentionally skipped.
func RunFromOptions(ctx context.Context, g *flags.GlobalFlags, opts Options) error {
	cmd := buildUpCmd(g, opts)
	if err := cmd.validate(); err != nil {
		return err
	}
	// Read from the copy in cmd.GlobalFlags so opts.Provider overrides take effect.
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return fmt.Errorf("load devsy config: %w", err)
	}
	cmd.applyConfig(devsyConfig)

	if cmd.Provider == "" && devsyConfig.Current().DefaultProvider == "" {
		return fmt.Errorf("no provider specified and no default provider configured for context %q",
			cmd.Context)
	}

	args := []string{opts.Source}
	client, err := cmd.prepareClient(ctx, devsyConfig, args)
	if err != nil {
		return fmt.Errorf("prepare workspace client: %w", err)
	}
	if err := cmd.checkExtraDevContainerProvider(client); err != nil {
		return err
	}
	telemetry.FromContext(ctx).SetClient(client)
	if err := cmd.Run(ctx, devsyConfig, client, args); err != nil {
		return err
	}
	recordWorkspaceGauge(ctx, devsyConfig)
	return nil
}

func recordWorkspaceGauge(ctx context.Context, devsyConfig *config.Config) {
	count, err := workspace.CountLocalWorkspaces(devsyConfig.DefaultContext)
	if err != nil {
		log.Debugf("skipping workspace count gauge: %v", err)
		return
	}
	telemetry.FromContext(ctx).RecordWorkspaceGauge(count)
}

func buildUpCmd(g *flags.GlobalFlags, opts Options) *UpCmd {
	ide := opts.IDE
	if ide == "" {
		ide = "none"
	}
	// Shallow-copy so per-call overrides don't mutate the caller's flags.
	gCopy := *g
	if gCopy.ResultFormat == "" {
		gCopy.ResultFormat = "plain"
	}
	if opts.Provider != "" {
		gCopy.Provider = opts.Provider
	}
	cmd := &UpCmd{
		GlobalFlags: &gCopy,
		Out:         io.Discard, // any callers wanting envelopes can set this themselves.
	}
	cmd.IDE = ide
	cmd.Source = opts.Source
	cmd.DevContainerPath = opts.DevcontainerPath
	cmd.DevContainerSource = opts.DevContainerSource
	cmd.RunArgs = opts.RunArgs
	cmd.ContainerEnv = opts.ContainerEnv
	cmd.RemoteUser = opts.RemoteUser
	if opts.Name != "" {
		cmd.ID = opts.Name
	}
	// *bool flags lose their CLI default when the cobra registration is skipped.
	mountGitRootDefault := true
	cmd.MountWorkspaceGitRoot = &mountGitRootDefault
	return cmd
}

// NewUpCmd creates a new up command.
func NewUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{GlobalFlags: f}
	upCmd := &cobra.Command{
		Use:   "up [flags] [workspace-path|workspace-name]",
		Short: "Start a workspace",
		RunE:  cmd.execute,
	}
	cmd.registerFlags(upCmd)
	upCmd.MarkFlagsMutuallyExclusive(names.NoLockfile, names.FrozenLockfile)
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

	out := cmd.stdout()
	cmd.statusReporter = newStatusReporter(emitJSON, out)

	t, err := cmd.setUpTask(client, emitJSON, out)
	if err != nil {
		return err
	}

	wctx, err := cmd.executeDevsyUp(ctx, devsyConfig, client)
	if err != nil {
		failTask(t, err)
		return reportErr(err, emitJSON, out)
	}
	if wctx == nil || cmd.Prebuild {
		succeedTask(t, nil)
		return nil // Platform mode or prebuild-only run.
	}

	err = cmd.finalizeUp(ctx, &finalizeUpArgs{
		devsyConfig: devsyConfig,
		client:      client,
		wctx:        wctx,
		emitJSON:    emitJSON,
		out:         out,
	})
	if err != nil {
		failTask(t, err)
		return err
	}
	succeedTask(t, wctx.result)
	return nil
}

// setUpTask opens the run's task (if any), tees the status reporter into it,
// and records the workspace ID on it.
func (cmd *UpCmd) setUpTask(
	client client2.BaseWorkspaceClient,
	emitJSON bool,
	out io.Writer,
) (*task.Task, error) {
	t, err := cmd.openTask()
	if err != nil {
		return nil, reportErr(err, emitJSON, out)
	}
	if t != nil {
		cmd.statusReporter = status.Tee(cmd.statusReporter, t.Reporter())
		if err := t.SetWorkspaceID(client.Workspace()); err != nil {
			failTask(t, err)
			return nil, reportErr(err, emitJSON, out)
		}
	}
	return t, nil
}

// reporter falls back to a no-op when Run hasn't set one yet.
func (cmd *UpCmd) reporter() status.Reporter {
	if cmd.statusReporter == nil {
		return status.Nop()
	}
	return cmd.statusReporter
}

func (cmd *UpCmd) checkExtraDevContainerProvider(client client2.BaseWorkspaceClient) error {
	if cmd.ExtraDevContainerPath != "" && client.Provider() != "docker" {
		return fmt.Errorf("extra devcontainer file is only supported with local provider")
	}
	return nil
}

func (cmd *UpCmd) applyConfig(devsyConfig *config.Config) {
	if devsyConfig.ContextOptionBool(config.ContextOptionSSHStrictHostKeyChecking) {
		cmd.StrictHostKeyChecking = true
	}
	cmd.resolveDotfilesOptions(devsyConfig)
}

type finalizeUpArgs struct {
	devsyConfig *config.Config
	client      client2.BaseWorkspaceClient
	wctx        *workspaceContext
	emitJSON    bool
	out         io.Writer
}

// finalizeUp performs the post-up steps: workspace configuration, optional SSH
// tunnel, IDE launch, and JSON envelope emission. Split out to keep Run small.
func (cmd *UpCmd) finalizeUp(ctx context.Context, args *finalizeUpArgs) error {
	if err := cmd.configureWorkspace(args.devsyConfig, args.client, args.wctx); err != nil {
		return reportErr(err, args.emitJSON, args.out)
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
		log.Warnf("failed to reconfigure ssh with tunnel port: %v", err)
	}

	ideURL, err := cmd.openIDE(ctx, args.devsyConfig, args.client, args.wctx)
	if err != nil {
		return reportErr(err, args.emitJSON, args.out)
	}
	if args.emitJSON {
		emitUpResult(args.wctx, ideURL, args.out)
	}
	if args.wctx.tunnelPort > 0 {
		log.Infof(
			"ssh tunnel active on port %d, waiting for shutdown signal",
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
		log.Warnf("failed to start ssh tunnel, falling back to ProxyCommand: %v", err)
		return nil
	}
	wctx.tunnelPort = tunnelPort
	return tunnelCleanup
}

func (cmd *UpCmd) stdout() io.Writer {
	if cmd.Out != nil {
		return cmd.Out
	}
	return os.Stdout
}

// reportErr writes the error to JSON output when requested and returns it for the caller.
func reportErr(err error, emitJSON bool, out io.Writer) error {
	if emitJSON {
		_ = config2.WriteErrorJSON(out, err.Error())
	}
	return err
}

// emitUpResult writes the JSON result envelope for a completed `up` invocation.
func emitUpResult(wctx *workspaceContext, ideURL string, out io.Writer) {
	containerID := config2.GetContainerID(wctx.result)
	var warnings []string
	recovery := false
	if wctx.result != nil {
		warnings = wctx.result.HostWarnings
		recovery = wctx.result.RecoveryContainer
	}
	_ = config2.WriteResultJSON(out, config2.ResultEnvelope{
		ContainerID:           containerID,
		RemoteUser:            wctx.user,
		RemoteWorkspaceFolder: wctx.workdir,
		URL:                   ideURL,
		Warnings:              warnings,
		Recovery:              recovery,
	})
}

func (cmd *UpCmd) execute(cobraCmd *cobra.Command, args []string) error {
	if err := cmd.validate(); err != nil {
		return err
	}
	if cmd.Detach && cmd.taskID == "" {
		return cmd.runDetached(args)
	}
	cmd.applyPullFromInsideContainerOverride(cobraCmd)
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
	if err := cmd.checkExtraDevContainerProvider(client); err != nil {
		return err
	}

	telemetry.FromContext(cobraCmd.Context()).SetClient(client)
	if err := cmd.Run(ctx, devsyConfig, client, args); err != nil {
		return err
	}
	recordWorkspaceGauge(ctx, devsyConfig)
	return nil
}

// workspaceContext holds the result of workspace preparation.
type workspaceContext struct {
	result     *config2.Result
	user       string
	workdir    string
	tunnelPort int
}

func (cmd *UpCmd) applyPullFromInsideContainerOverride(cobraCmd *cobra.Command) {
	if !cobraCmd.Flags().Changed(names.PullFromInsideContainer) {
		return
	}
	value := cmd.pullFromInsideContainerFlag
	cmd.PullFromInsideContainerOverride = &value
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
		log.Debug("reusing SSH_AUTH_SOCK", cmd.SSHAuthSockID)
	} else if cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		log.Debug(
			"reusing SSH_AUTH_SOCK is not supported with platform mode, consider launching the IDE from the platform UI",
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
	if err := validateUpResult(result, err); err != nil {
		return nil, err
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
	workdir := cmd.resolveWorkdir(result, client)
	return &workspaceContext{result: result, user: user, workdir: workdir}, nil
}

// validateUpResult turns the (result, err) pair from devsyUp into a single
// error. It prefers the structured message the agent forwarded in the result
// over the generic transport error so callers see the actual cause; when both
// are present the transport error is wrapped for the full chain.
func validateUpResult(result *config2.Result, err error) error {
	if resultErr := result.Err(); resultErr != nil {
		if err != nil {
			return fmt.Errorf("start workspace: %w: %w", resultErr, err)
		}
		return fmt.Errorf("start workspace: %w", resultErr)
	}
	if err != nil {
		return fmt.Errorf("start workspace: %w", err)
	}
	if result == nil {
		return config2.ErrNoAgentResult
	}
	return nil
}

// resolveWorkdir determines the container workspace folder, honoring a git
// subpath and an explicit --workspace-folder override.
func (cmd *UpCmd) resolveWorkdir(
	result *config2.Result,
	client client2.BaseWorkspaceClient,
) string {
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
	return workdir
}

var shutdownSignals = []os.Signal{
	os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT,
}

// WithSignals returns a context cancelled on the first shutdown signal so
// deferred cleanup can run, and forces exit on a second signal. The returned
// func must be called to release the signal handler.
func WithSignals(ctx context.Context) (context.Context, func()) {
	ctx, stop := signal.NotifyContext(ctx, shutdownSignals...)

	// done lets the force-shutdown watcher exit once cleanup runs; stop()
	// alone wouldn't wake it.
	done := make(chan struct{})
	go watchForForceShutdown(ctx, done)

	return ctx, func() {
		stop()
		close(done)
	}
}

// watchForForceShutdown waits for the first-signal cancellation, then forces
// exit if a second signal arrives before cleanup closes done.
func watchForForceShutdown(ctx context.Context, done <-chan struct{}) {
	select {
	case <-ctx.Done():
	case <-done:
		return
	}
	// Skip the second-signal wait if cleanup already closed done.
	select {
	case <-done:
		return
	default:
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, shutdownSignals...)
	select {
	case <-signals:
		os.Exit(1) // second signal — force shutdown
	case <-done:
		signal.Stop(signals)
	}
}
