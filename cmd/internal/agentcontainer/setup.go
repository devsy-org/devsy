//go:build !windows

package agentcontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/compress"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/spf13/cobra"
)

// SetupContainerCmd holds the cmd flags.
type SetupContainerCmd struct {
	*flags.GlobalFlags

	ChownWorkspace         bool
	StreamMounts           bool
	InjectGitCredentials   bool
	Prebuild               bool
	ContainerWorkspaceInfo string
	SetupInfo              string
	AccessKey              string
	PlatformHost           string
	WorkspaceHost          string
	DotfilesRepo           string
	DotfilesScript         string
}

// NewSetupContainerCmd creates a new command.
func NewSetupContainerCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupContainerCmd{
		GlobalFlags: globalFlags,
	}
	setupContainerCmd := &cobra.Command{
		Use:   "setup",
		Short: "Sets up a container",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cmd.registerFlags(setupContainerCmd)

	return setupContainerCmd
}

type containerState struct {
	workspaceInfo *provider2.ContainerWorkspaceInfo
	setupInfo     *config.Result
	tunnelClient  tunnel.TunnelClient
	reporter      status.Reporter
	secretsEnv    []string
	secretsMount  []string
	lifecycle     *lifecycleHandshake
	run           *containerRun
}

// Run runs the command logic.
func (cmd *SetupContainerCmd) Run(ctx context.Context) error {
	tunnelClient, err := cmd.initializeTunnelClient(ctx)
	if err != nil {
		return err
	}

	workspaceInfo, setupInfo, err := cmd.parseWorkspaceAndSetupInfo()
	if err != nil {
		return err
	}

	state := &containerState{
		workspaceInfo: workspaceInfo,
		setupInfo:     setupInfo,
		tunnelClient:  tunnelClient,
		reporter:      tunnelserver.NewTunnelStatusReporter(ctx, tunnelClient),
	}

	if !cmd.Prebuild {
		state.lifecycle, err = beginLifecycle(
			ctx,
			config2.ContainerLifecycleDir,
			workspaceInfo,
			setupInfo.MergedConfig.ShutdownAction,
		)
		if err != nil {
			return err
		}
	}
	if state.lifecycle != nil {
		ctx = state.lifecycle.ctx
		defer state.lifecycle.close()
	}

	_, err = tunnelserver.ReportResult(
		ctx,
		tunnelClient,
		func(reportCtx context.Context) (*config.Result, error) {
			return cmd.reportSetup(reportCtx, state)
		},
	)
	if state.lifecycle != nil {
		return state.lifecycle.finish(err)
	}
	return err
}

func (cmd *SetupContainerCmd) reportSetup(
	ctx context.Context,
	state *containerState,
) (*config.Result, error) {
	ctx, err := cmd.beginRun(ctx, state)
	if err != nil {
		return nil, err
	}
	hop := state.run.mirror(ctx, state.tunnelClient)
	defer hop.Close()
	err = cmd.runSetup(ctx, state)
	state.run.finish(err)
	// Drain before the result is reported and stdio is torn down.
	state.run.drain(ctx, hop)
	if err != nil {
		return nil, err
	}
	return state.setupInfo, nil
}

// beginRun fetches the workspace secrets once and starts the run's recorder
// with them so their values are redacted before anything is stored.
func (cmd *SetupContainerCmd) beginRun(
	ctx context.Context,
	state *containerState,
) (context.Context, error) {
	env, mount, err := fetchSecrets(ctx, state.tunnelClient)
	if err != nil {
		return ctx, err
	}
	state.secretsEnv, state.secretsMount = env, mount
	state.run = startContainerRun(state.workspaceInfo, append(slices.Clone(env), mount...))
	if state.run == nil {
		return ctx, nil
	}

	return state.run.session.Context(ctx), nil
}

func (cmd *SetupContainerCmd) runSetup(ctx context.Context, state *containerState) error {
	if err := cmd.prepareWorkspace(ctx, state); err != nil {
		return err
	}

	return cmd.finalizeSetup(ctx, state)
}

func (cmd *SetupContainerCmd) registerFlags(setupContainerCmd *cobra.Command) {
	cmd.registerBehaviorFlags(setupContainerCmd)
	cmd.registerWorkspaceInfoFlags(setupContainerCmd)
	cmd.registerDotfilesFlags(setupContainerCmd)
}

func (cmd *SetupContainerCmd) registerBehaviorFlags(setupContainerCmd *cobra.Command) {
	cliflags.Add(
		setupContainerCmd,
		cliflags.Bool(
			&cmd.StreamMounts,
			names.StreamMounts,
			false,
			"If true, will try to stream the bind mounts from the host",
		),
		cliflags.Bool(
			&cmd.Prebuild,
			names.Prebuild,
			false,
			"If true, only run prebuild lifecycle hooks (onCreateCommand + updateContentCommand)",
		),
		cliflags.Bool(
			&cmd.ChownWorkspace,
			names.ChownWorkspace,
			false,
			"If Devsy should chown the workspace to the remote user",
		),
		cliflags.Bool(
			&cmd.InjectGitCredentials,
			names.InjectGitCredentials,
			false,
			"If Devsy should inject git credentials during setup",
		),
	)
}

func (cmd *SetupContainerCmd) registerWorkspaceInfoFlags(setupContainerCmd *cobra.Command) {
	cliflags.Add(
		setupContainerCmd,
		cliflags.String(
			&cmd.ContainerWorkspaceInfo,
			names.ContainerWorkspaceInfo,
			"",
			"The container workspace info",
		),
		cliflags.String(&cmd.SetupInfo, names.SetupInfo, "", "The container setup info"),
		cliflags.String(&cmd.AccessKey, names.AccessKey, "", "Access Key to use"),
		cliflags.String(&cmd.WorkspaceHost, names.WorkspaceHost, "", "Workspace hostname to use"),
		cliflags.String(&cmd.PlatformHost, names.PlatformHost, "", "Platform host"),
	)
	_ = setupContainerCmd.MarkFlagRequired(names.SetupInfo)

	cliflags.BindEnv(setupContainerCmd.Flags(), names.AccessKey)
	cliflags.BindEnv(setupContainerCmd.Flags(), names.PlatformHost)
}

func (cmd *SetupContainerCmd) registerDotfilesFlags(setupContainerCmd *cobra.Command) {
	cliflags.Add(
		setupContainerCmd,
		cliflags.String(&cmd.DotfilesRepo, names.DotfilesRepo, "", "Dotfiles repository URL"),
		cliflags.String(
			&cmd.DotfilesScript,
			names.DotfilesScript,
			"",
			"Dotfiles install script path",
		),
	)
}

func (cmd *SetupContainerCmd) prepareWorkspace(
	ctx context.Context,
	state *containerState,
) error {
	if err := cmd.syncMounts(ctx, state); err != nil {
		return err
	}

	if err := agent.DockerlessBuild(agent.DockerlessBuildOptions{
		Context:           ctx,
		SetupInfo:         state.setupInfo,
		DockerlessOptions: &state.workspaceInfo.Dockerless,
		ImageConfigOutput: agent.DefaultImageConfigPath,
		Debug:             cmd.Debug,
		ConfigureCredentialsFunc: func(ctx context.Context) (string, error) {
			serverPort, err := credentials.StartCredentialsServer(
				ctx,
				state.tunnelClient,
			)
			if err != nil {
				return "", err
			}
			return dockercredentials.ConfigureCredentialsDockerless(
				agent.DockerlessCredentialsPath,
				serverPort,
			)
		},
	}); err != nil {
		return fmt.Errorf("dockerless build: %w", err)
	}

	if err := fillContainerEnv(state.setupInfo); err != nil {
		return err
	}

	cleanupFunc := cmd.setupGitCredentials(
		ctx,
		state.tunnelClient,
		config.GetRemoteUser(state.setupInfo),
	)

	cloneErr := cmd.cloneRepositoryIfNeeded(
		ctx,
		state.workspaceInfo,
		state.setupInfo,
		state.reporter,
	)

	if cleanupFunc != nil {
		cleanupFunc()
	}

	return cloneErr
}

// fetchSecrets pulls secrets over the tunnel.
func fetchSecrets(
	ctx context.Context,
	client tunnel.TunnelClient,
) (env, mount []string, err error) {
	resp, err := client.Secrets(ctx, &tunnel.Empty{})
	if err != nil {
		return nil, nil, fmt.Errorf("fetch secrets: %w", err)
	}

	for _, secret := range resp.GetSecrets() {
		entry := secret.GetName() + "=" + secret.GetValue()
		if secret.GetMount() {
			mount = append(mount, entry)
		} else {
			env = append(env, entry)
		}
	}

	return env, mount, nil
}

func (cmd *SetupContainerCmd) finalizeSetup(ctx context.Context, state *containerState) error {
	secretsEnv, secretsMount := state.secretsEnv, state.secretsMount

	cfg := &setup.ContainerSetupConfig{
		SetupInfo:              state.setupInfo,
		ExtraWorkspaceEnv:      state.workspaceInfo.CLIOptions.WorkspaceEnv,
		SecretsEnv:             secretsEnv,
		TerminalSecretEnvNames: state.workspaceInfo.CLIOptions.TerminalSecretEnvNames,
		SecretsMount:           secretsMount,
		ChownProjects:          cmd.ChownWorkspace,
		PlatformOptions:        &state.workspaceInfo.CLIOptions.Platform,
		TunnelClient:           state.tunnelClient,
		Prebuild:               cmd.Prebuild,
		SkipPostCreate:         state.workspaceInfo.CLIOptions.SkipPostCreate,
		SkipPostStart:          state.workspaceInfo.CLIOptions.SkipPostStart,
		SkipPostAttach:         state.workspaceInfo.CLIOptions.SkipPostAttach,
		WaitFor:                setup.LifecyclePhase(state.workspaceInfo.CLIOptions.WaitFor),
		Dotfiles: setup.DotfilesConfig{
			Repository:    cmd.DotfilesRepo,
			InstallScript: cmd.DotfilesScript,
			RemoteUser:    config.GetRemoteUser(state.setupInfo),
		},
	}

	deferred, err := setup.SetupContainerPreAttach(ctx, cfg)
	if err != nil {
		return err
	}

	if !cmd.Prebuild {
		if err := cmd.setupPostAttach(state, deferred); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *SetupContainerCmd) setupPostAttach(
	state *containerState,
	deferred setup.DeferredHooks,
) error {
	if err := cmd.installIDE(state.setupInfo, &state.workspaceInfo.IDE); err != nil {
		return err
	}

	if err := cmd.startDaemonUnlessLeased(state); err != nil {
		return err
	}

	resolvedSetupInfo, err := compressSetupInfo(state.setupInfo)
	if err != nil {
		return fmt.Errorf("re-serialize setup info: %w", err)
	}

	workers, err := newLeasedWorkerReadiness(state)
	if err != nil {
		return err
	}
	if workers != nil {
		defer workers.close()
	}

	state.run.reserveWorkers(
		!deferred.Empty(),
		len(state.setupInfo.MergedConfig.PostAttachCommands) > 0,
	)
	cmd.launchWorkers(state, deferred, resolvedSetupInfo, workers)
	state.run.sealDiscovery()

	return armLifecycle(state, workers)
}

// launchWorkers starts the deferred and post-attach hook workers. A launch
// failure is logged; the workers are optional and the workspace stays usable.
func (cmd *SetupContainerCmd) launchWorkers(
	state *containerState,
	deferred setup.DeferredHooks,
	resolvedSetupInfo string,
	workers *workerReadiness,
) {
	if !deferred.Empty() {
		if err := cmd.startDeferredHooks(state, resolvedSetupInfo, workers); err != nil {
			log.Errorf("failed to start deferred lifecycle hooks: %v", err)
		}
	}

	if err := cmd.startPostAttachHooks(state, workers); err != nil {
		log.Errorf("failed to start postAttachCommand: %v", err)
	}
}

func (cmd *SetupContainerCmd) startDaemonUnlessLeased(state *containerState) error {
	if state.lifecycle != nil {
		return nil
	}
	shutdownAction := state.setupInfo.MergedConfig.ShutdownAction
	return cmd.startContainerDaemon(state.workspaceInfo, shutdownAction)
}

func newLeasedWorkerReadiness(state *containerState) (*workerReadiness, error) {
	if state.lifecycle == nil {
		return nil, nil
	}
	return newWorkerReadiness()
}

// armLifecycle runs last, once the spawned workers hold the workspace active themselves.
func armLifecycle(state *containerState, workers *workerReadiness) error {
	if state.lifecycle == nil {
		return nil
	}
	if err := workers.wait(state.lifecycle.ctx); err != nil {
		log.Errorf("hook protection not confirmed before arming: %v", err)
	}
	return state.lifecycle.ready()
}

// compressSetupInfo marshals and compresses a config.Result for passing
// to background subprocesses.
func compressSetupInfo(setupInfo *config.Result) (string, error) {
	raw, err := json.Marshal(setupInfo)
	if err != nil {
		return "", fmt.Errorf("marshal setup info: %w", err)
	}
	return compress.Compress(string(raw))
}

func (cmd *SetupContainerCmd) startDeferredHooks(
	state *containerState,
	setupInfo string,
	workers *workerReadiness,
) error {
	built := false
	err := workers.launch("deferred-hooks", func(prepare func(*exec.Cmd)) error {
		return command.StartBackgroundOnce("devsy.deferred-hooks", func() (*exec.Cmd, error) {
			log.Debugf("starting deferred lifecycle hooks as background process")
			execCmd, err := buildDeferredHooksCmd(deferredHooksArgs{
				setupInfo:      setupInfo,
				prebuild:       cmd.Prebuild,
				dotfilesRepo:   cmd.DotfilesRepo,
				dotfilesScript: cmd.DotfilesScript,
				secretsEnv:     state.secretsEnv,
			})
			if err != nil {
				return nil, err
			}
			execCmd.Env = state.run.workerEnv(execCmd.Env, state.run.deferredChild())
			built = true
			prepare(execCmd)
			return execCmd, nil
		})
	})
	state.run.launchOutcome(state.run.deferredChild(), built, err)

	return err
}

type deferredHooksArgs struct {
	setupInfo      string
	prebuild       bool
	dotfilesRepo   string
	dotfilesScript string
	secretsEnv     []string
}

func buildDeferredHooksCmd(a deferredHooksArgs) (*exec.Cmd, error) {
	binaryPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := []string{
		cmdInternal, cmdAgent, cmdContainer, "deferred-hooks",
		names.Flag(names.SetupInfo), a.setupInfo,
	}
	if a.prebuild {
		args = append(args, names.Flag(names.Prebuild))
	}
	if a.dotfilesRepo != "" {
		args = append(args, names.Flag(names.DotfilesRepo), a.dotfilesRepo)
	}
	if a.dotfilesScript != "" {
		args = append(args, names.Flag(names.DotfilesScript), a.dotfilesScript)
	}
	cmd := &exec.Cmd{
		Path: binaryPath,
		Args: append([]string{binaryPath}, args...),
	}
	cmd.Env = secretsEnvOverride(a.secretsEnv)

	return cmd, nil
}

func (cmd *SetupContainerCmd) initializeTunnelClient(
	ctx context.Context,
) (tunnel.TunnelClient, error) {
	tunnelClient, err := tunnelserver.NewTunnelClient(os.Stdin, os.Stdout)
	if err != nil {
		return nil, fmt.Errorf("initializing tunnel client: %w", err)
	}

	log.Debugf("created logger")

	if _, err := tunnelClient.Ping(ctx, &tunnel.Empty{}); err != nil {
		return nil, fmt.Errorf("ping client: %w", err)
	}

	return tunnelClient, nil
}

func (cmd *SetupContainerCmd) parseWorkspaceAndSetupInfo() (*provider2.ContainerWorkspaceInfo, *config.Result, error) {
	log.Debugf("begin setting up container")
	workspaceInfo, _, err := agent.DecodeContainerWorkspaceInfo(cmd.ContainerWorkspaceInfo)
	if err != nil {
		return nil, nil, err
	}

	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return nil, nil, err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return nil, nil, err
	}

	return workspaceInfo, setupInfo, nil
}

func (cmd *SetupContainerCmd) setupGitCredentials(
	ctx context.Context,
	tunnelClient tunnel.TunnelClient,
	remoteUser string,
) func() {
	if !cmd.InjectGitCredentials {
		return nil
	}

	if !command.Exists("git") {
		log.Debugf("git not found, skipping git credentials configuration")
		return nil
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cleanupFunc, err := configureSystemGitCredentials(
		cancelCtx,
		tunnelClient,
		remoteUser,
	)
	if err != nil {
		cancel()
		log.Errorf("error configuring git credentials: %v", err)
		return nil
	}

	return func() {
		cleanupFunc()
		cancel()
	}
}

func (cmd *SetupContainerCmd) cloneRepositoryIfNeeded(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	setupInfo *config.Result,
	reporter status.Reporter,
) error {
	b, err := workspaceInfo.PullFromInsideContainer.Bool()
	if err != nil {
		return fmt.Errorf("parse pullFromInsideContainer: %w", err)
	}
	if !b {
		return nil
	}

	gitPath := filepath.Join(setupInfo.SubstitutionContext.ContainerWorkspaceFolder, ".git")
	if _, err := os.Stat(gitPath); err == nil && !workspaceInfo.CLIOptions.Recreate {
		log.Debugf(
			"workspace repository already checked out %s, skipping clone",
			setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
		)
		return nil
	}

	return agent.CloneRepositoryForWorkspace(ctx, agent.CloneWorkspaceParams{
		Source:           &workspaceInfo.Source,
		AgentConfig:      &workspaceInfo.Agent,
		WorkspaceDir:     setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
		Options:          workspaceInfo.CLIOptions,
		OverwriteContent: true,
		Reporter:         reporter,
	})
}

func (cmd *SetupContainerCmd) startContainerDaemon(
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	shutdownAction string,
) error {
	// A managed container's main process owns the timer; never start a second one.
	if isManagedLifecycle(workspaceInfo) {
		return nil
	}
	if workspaceInfo.CLIOptions.Platform.Enabled ||
		workspaceInfo.CLIOptions.DisableDaemon ||
		workspaceInfo.ContainerTimeout == "" {
		return nil
	}

	return command.StartBackgroundOnce(config2.BinaryName+".daemon", func() (*exec.Cmd, error) {
		log.Debugf(
			"start %s container daemon with inactivity timeout %s",
			config2.BinaryName,
			workspaceInfo.ContainerTimeout,
		)
		binaryPath, err := os.Executable()
		if err != nil {
			return nil, err
		}

		args := []string{
			cmdInternal, cmdAgent, cmdContainer, "daemon",
			names.Flag(names.Timeout), workspaceInfo.ContainerTimeout,
		}
		if shutdownAction != "" {
			args = append(args, names.Flag(names.ShutdownAction), shutdownAction)
		}

		daemonCmd := &exec.Cmd{
			Path: binaryPath,
			Args: append([]string{binaryPath}, args...),
		}
		return daemonCmd, nil
	})
}

func (cmd *SetupContainerCmd) startPostAttachHooks(
	state *containerState,
	workers *workerReadiness,
) error {
	if len(state.setupInfo.MergedConfig.PostAttachCommands) == 0 {
		return nil
	}

	built := false
	err := workers.launch("post-attach", func(prepare func(*exec.Cmd)) error {
		return command.StartBackground("devsy.post-attach", func() (*exec.Cmd, error) {
			log.Debugf("starting postAttachCommand as background process")
			binaryPath, err := os.Executable()
			if err != nil {
				return nil, err
			}

			args := []string{
				cmdInternal, cmdAgent, cmdContainer, "post-attach",
				names.Flag(names.SetupInfo), cmd.SetupInfo,
			}
			execCmd := &exec.Cmd{
				Path: binaryPath,
				Args: append([]string{binaryPath}, args...),
			}
			execCmd.Env = secretsEnvOverride(state.secretsEnv)
			execCmd.Env = state.run.workerEnv(execCmd.Env, state.run.postAttachChild())
			built = true
			prepare(execCmd)
			return execCmd, nil
		})
	})
	state.run.launchOutcome(state.run.postAttachChild(), built, err)

	return err
}

func fillContainerEnv(setupInfo *config.Result) error {
	// set remote-env
	if setupInfo.MergedConfig.RemoteEnv == nil {
		setupInfo.MergedConfig.RemoteEnv = make(map[string]*string)
	}

	if _, ok := setupInfo.MergedConfig.RemoteEnv["PATH"]; !ok {
		pathVal := "${containerEnv:PATH}"
		setupInfo.MergedConfig.RemoteEnv["PATH"] = &pathVal
	}

	// merge config
	newMergedConfig := &config.MergedDevContainerConfig{}
	err := config.SubstituteContainerEnv(
		config.ListToObject(os.Environ()),
		setupInfo.MergedConfig,
		newMergedConfig,
	)
	if err != nil {
		return fmt.Errorf("substitute container env: %w", err)
	}
	setupInfo.MergedConfig = newMergedConfig
	return nil
}

func configureSystemGitCredentials(
	ctx context.Context,
	client tunnel.TunnelClient,
	remoteUser string,
) (func(), error) {
	serverPort, err := credentials.StartCredentialsServer(ctx, client)
	if err != nil {
		return nil, err
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	gitCredentials := fmt.Sprintf(
		"!'%s' internal agent git-credentials --port %d",
		binaryPath,
		serverPort,
	)
	_ = os.Setenv(config2.EnvGitHelperPort, strconv.Itoa(serverPort))

	gitConfig := git.At("/", git.WithStrictHostKeyChecking(false)).Config()
	gitConfig, scope, err := addGitCredentialHelper(
		ctx,
		gitConfig,
		gitCredentials,
		remoteUser,
	)
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		log.Debug("unset setup credential helper")
		if err = gitConfig.UnsetValue(ctx, "credential.helper", gitCredentials, scope); err != nil {
			log.Errorf("unset credential helper %v", err)
		}
	}

	return cleanup, nil
}

// addGitCredentialHelper installs the credential helper system-wide
// (/etc/gitconfig) so it applies regardless of which local user's git
// invocation picks it up. A container's remoteUser can differ from the
// process configuring it. Falls back to remoteUser's global config
// when /etc/gitconfig is not writable (e.g. a non-root OpenShift-style pod
// running as a single fixed UID, where that multi-user concern doesn't
// apply), returning the config and scope actually used so the caller unsets
// the same one.
func addGitCredentialHelper(
	ctx context.Context,
	gitConfig *git.Config,
	value string,
	remoteUser string,
) (*git.Config, git.ConfigScope, error) {
	err := gitConfig.Add(ctx, "credential.helper", value, git.ScopeSystem)
	if err == nil {
		return gitConfig, git.ScopeSystem, nil
	}
	if !isGitPermissionDenied(err) {
		return nil, git.ConfigScope{}, fmt.Errorf("add git credential helper: %w", err)
	}

	homeDir, err := command.GetHome(remoteUser)
	if err != nil {
		return nil, git.ConfigScope{}, fmt.Errorf(
			"resolve remote user home for git credentials: %w",
			err,
		)
	}
	log.Debugf("system git config is not writable, falling back to %s's global config", remoteUser)
	globalGitConfig := git.At(
		"/",
		git.WithStrictHostKeyChecking(false),
		git.WithEnv([]string{
			"HOME=" + homeDir,
			"XDG_CONFIG_HOME=" + filepath.Join(homeDir, ".config"),
		}),
	).Config()
	if err := globalGitConfig.Add(ctx, "credential.helper", value, git.ScopeGlobal); err != nil {
		return nil, git.ConfigScope{}, fmt.Errorf("add git credential helper: %w", err)
	}
	return globalGitConfig, git.ScopeGlobal, nil
}

func isGitPermissionDenied(err error) bool {
	var cmdErr *git.CommandError
	return errors.As(err, &cmdErr) &&
		(strings.Contains(cmdErr.Stderr, "Permission denied") ||
			strings.Contains(cmdErr.Stderr, "Read-only file system"))
}
