//go:build !windows

package agentcontainer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	agentsnapshot "github.com/devsy-org/devsy/pkg/agent/snapshot"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/compress"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	"github.com/devsy-org/devsy/pkg/extract"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/ide/fleet"
	"github.com/devsy-org/devsy/pkg/ide/jetbrains"
	"github.com/devsy-org/devsy/pkg/ide/jupyter"
	"github.com/devsy-org/devsy/pkg/ide/marimo"
	"github.com/devsy-org/devsy/pkg/ide/rstudio"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ts"
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
	secretsEnv    []string
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
	}

	_, err = tunnelserver.ReportResult(
		ctx,
		tunnelClient,
		func(reportCtx context.Context) (*config.Result, error) {
			if err := cmd.prepareWorkspace(reportCtx, state); err != nil {
				return nil, err
			}
			if err := cmd.finalizeSetup(reportCtx, state); err != nil {
				return nil, err
			}
			return state.setupInfo, nil
		},
	)
	return err
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
	)

	cloneErr := cmd.cloneRepositoryIfNeeded(
		ctx,
		state.workspaceInfo,
		state.setupInfo,
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
	secretsEnv, secretsMount, err := fetchSecrets(ctx, state.tunnelClient)
	if err != nil {
		return err
	}
	state.secretsEnv = secretsEnv

	cfg := &setup.ContainerSetupConfig{
		SetupInfo:         state.setupInfo,
		ExtraWorkspaceEnv: state.workspaceInfo.CLIOptions.WorkspaceEnv,
		SecretsEnv:        secretsEnv,
		SecretsMount:      secretsMount,
		ChownProjects:     cmd.ChownWorkspace,
		PlatformOptions:   &state.workspaceInfo.CLIOptions.Platform,
		TunnelClient:      state.tunnelClient,
		Prebuild:          cmd.Prebuild,
		SkipPostCreate:    state.workspaceInfo.CLIOptions.SkipPostCreate,
		SkipPostStart:     state.workspaceInfo.CLIOptions.SkipPostStart,
		SkipPostAttach:    state.workspaceInfo.CLIOptions.SkipPostAttach,
		WaitFor:           setup.LifecyclePhase(state.workspaceInfo.CLIOptions.WaitFor),
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

	shutdownAction := state.setupInfo.MergedConfig.ShutdownAction
	if err := cmd.startContainerDaemon(state.workspaceInfo, shutdownAction); err != nil {
		return err
	}

	resolvedSetupInfo, err := compressSetupInfo(state.setupInfo)
	if err != nil {
		return fmt.Errorf("re-serialize setup info: %w", err)
	}

	if !deferred.Empty() {
		err = cmd.startDeferredHooks(
			resolvedSetupInfo, cmd.DotfilesRepo, cmd.DotfilesScript,
			state.secretsEnv,
		)
		if err != nil {
			log.Errorf("failed to start deferred lifecycle hooks: %v", err)
		}
	}

	if err := cmd.startPostAttachHooks(state); err != nil {
		log.Errorf("failed to start postAttachCommand: %v", err)
	}

	return nil
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
	setupInfo, dotfilesRepo, dotfilesScript string, secretsEnv []string,
) error {
	return command.StartBackgroundOnce("devsy.deferred-hooks", func() (*exec.Cmd, error) {
		log.Debugf("starting deferred lifecycle hooks as background process")
		return buildDeferredHooksCmd(
			setupInfo,
			cmd.Prebuild,
			dotfilesRepo,
			dotfilesScript,
			secretsEnv,
		)
	})
}

func buildDeferredHooksCmd(
	setupInfo string, prebuild bool, dotfilesRepo, dotfilesScript string, secretsEnv []string,
) (*exec.Cmd, error) {
	binaryPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := []string{
		cmdInternal, cmdAgent, cmdContainer, "deferred-hooks",
		names.Flag(names.SetupInfo), setupInfo,
	}
	if prebuild {
		args = append(args, names.Flag(names.Prebuild))
	}
	if dotfilesRepo != "" {
		args = append(args, names.Flag(names.DotfilesRepo), dotfilesRepo)
	}
	if dotfilesScript != "" {
		args = append(args, names.Flag(names.DotfilesScript), dotfilesScript)
	}
	cmd := &exec.Cmd{
		Path: binaryPath,
		Args: append([]string{binaryPath}, args...),
	}
	cmd.Env = secretsEnvOverride(secretsEnv)

	return cmd, nil
}

func (cmd *SetupContainerCmd) initializeTunnelClient(
	ctx context.Context,
) (tunnel.TunnelClient, error) {
	tunnelClient, err := tunnelserver.NewTunnelClient(os.Stdin, os.Stdout, true, 0)
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

func (cmd *SetupContainerCmd) syncMounts(ctx context.Context, state *containerState) error {
	mounts := config.GetMounts(state.setupInfo)
	if state.workspaceInfo.Source.Snapshot != "" {
		return restoreSnapshotMounts(ctx, state, mounts)
	}

	if !cmd.StreamMounts {
		return nil
	}

	log.Debugf("syncing mounts: %v", mounts)
	for _, m := range mounts {
		if !state.workspaceInfo.CLIOptions.Reset {
			files, err := os.ReadDir(m.Target)
			if err == nil && len(files) > 0 {
				log.Debugf("skip stream mount %s because it is not empty", m.Target)
				continue
			}
		}

		if err := streamMount(
			ctx,
			state.workspaceInfo,
			m,
			state.tunnelClient,
		); err != nil {
			return err
		}
	}

	return nil
}

// restoreSnapshotMounts restores a snapshot-sourced workspace's volumes,
// skipping the restore if the sole mount target already has real content.
func restoreSnapshotMounts(
	ctx context.Context,
	state *containerState,
	mounts []*config.Mount,
) error {
	if !state.workspaceInfo.CLIOptions.Reset && len(mounts) == 1 &&
		skipSnapshotRestore(mounts[0].Target) {
		return nil
	}
	log.Infof("restoring snapshot volumes from %s", state.workspaceInfo.Source.Snapshot)
	if err := agentsnapshot.RestoreVolumes(
		ctx,
		state.workspaceInfo.Source.Snapshot,
		mounts,
		state.workspaceInfo.CLIOptions.Reset,
	); err != nil {
		return fmt.Errorf("restore snapshot volumes: %w", err)
	}
	return nil
}

// synthesizedDevContainerName is the devcontainer.json devsy synthesizes for
// image/none-sourced workspaces (pkg/devcontainer's saveSynthesizedConfig). A
// snapshot-sourced restore gets one too, written into the mount target before
// syncMounts ever runs, so it must not count as "already has content" below.
var synthesizedDevContainerName = ".devcontainer." + config2.BinaryName + ".json"

// skipSnapshotRestore reports whether target already has real content, in
// which case a snapshot restore into it would be destructive and is skipped.
func skipSnapshotRestore(target string) bool {
	files, err := os.ReadDir(target)
	if err != nil {
		return false
	}
	var names []string
	for _, f := range files {
		if f.Name() == synthesizedDevContainerName {
			continue
		}
		names = append(names, f.Name())
	}
	if len(names) == 0 {
		return false
	}
	log.Debugf("skip snapshot restore because %s is not empty: entries=%v", target, names)
	return true
}

func (cmd *SetupContainerCmd) setupGitCredentials(
	ctx context.Context,
	tunnelClient tunnel.TunnelClient,
) func() {
	if !cmd.InjectGitCredentials {
		return nil
	}

	if !command.Exists("git") {
		log.Debugf("git not found, skipping git credentials configuration")
		return nil
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cleanupFunc, err := configureSystemGitCredentials(cancelCtx, tunnelClient)
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
	})
}

func (cmd *SetupContainerCmd) startContainerDaemon(
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	shutdownAction string,
) error {
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

func (cmd *SetupContainerCmd) startPostAttachHooks(state *containerState) error {
	if len(state.setupInfo.MergedConfig.PostAttachCommands) == 0 {
		return nil
	}

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
		return execCmd, nil
	})
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

var vscodeFlavors = map[string]vscode.Flavor{
	string(config2.IDEVSCode):         vscode.FlavorStable,
	string(config2.IDEVSCodeInsiders): vscode.FlavorInsiders,
	string(config2.IDECursor):         vscode.FlavorCursor,
	string(config2.IDEPositron):       vscode.FlavorPositron,
	string(config2.IDECodium):         vscode.FlavorCodium,
	string(config2.IDEWindsurf):       vscode.FlavorWindsurf,
	string(config2.IDEAntigravity):    vscode.FlavorAntigravity,
	string(config2.IDEBob):            vscode.FlavorBob,
}

type jetbrainsServerFactory func(
	string,
	map[string]config2.OptionValue,
) *jetbrains.GenericJetBrainsServer

var jetbrainsServers = map[string]jetbrainsServerFactory{
	string(config2.IDEGoland):    jetbrains.NewGolandServer,
	string(config2.IDERustRover): jetbrains.NewRustRoverServer,
	string(config2.IDEPyCharm):   jetbrains.NewPyCharmServer,
	string(config2.IDEPhpStorm):  jetbrains.NewPhpStorm,
	string(config2.IDEIntellij):  jetbrains.NewIntellij,
	string(config2.IDECLion):     jetbrains.NewCLionServer,
	string(config2.IDERider):     jetbrains.NewRiderServer,
	string(config2.IDERubyMine):  jetbrains.NewRubyMineServer,
	string(config2.IDEWebStorm):  jetbrains.NewWebStormServer,
	string(config2.IDEDataSpell): jetbrains.NewDataSpellServer,
}

func (cmd *SetupContainerCmd) installIDE(
	setupInfo *config.Result,
	ide *provider2.WorkspaceIDEConfig,
) error {
	if flavor, ok := vscodeFlavors[ide.Name]; ok {
		return cmd.setupVSCode(setupInfo, ide.Options, flavor)
	}
	if newServer, ok := jetbrainsServers[ide.Name]; ok {
		return newServer(config.GetRemoteUser(setupInfo), ide.Options).Install(setupInfo)
	}

	switch ide.Name {
	case string(config2.IDENone):
		return nil
	case string(config2.IDEOpenVSCode), string(config2.IDECodeServer), string(config2.IDEVSCodeWeb):
		return cmd.setupBrowserIDE(ide.Name, setupInfo, ide.Options)
	}

	return installNotebookIDE(setupInfo, ide)
}

func installNotebookIDE(
	setupInfo *config.Result,
	ide *provider2.WorkspaceIDEConfig,
) error {
	user := config.GetRemoteUser(setupInfo)
	folder := setupInfo.SubstitutionContext.ContainerWorkspaceFolder
	switch ide.Name {
	case string(config2.IDEFleet):
		return fleet.NewFleetServer(user, ide.Options).Install(folder)
	case string(config2.IDEJupyterNotebook):
		return jupyter.NewJupyterNotebookServer(folder, user, ide.Options).Install()
	case string(config2.IDEMarimo):
		return marimo.NewMarimoServer(folder, user, ide.Options).Install()
	case string(config2.IDERStudio):
		return rstudio.NewRStudioServer(folder, user, ide.Options).Install()
	}

	return nil
}

func (cmd *SetupContainerCmd) setupVSCode(
	setupInfo *config.Result,
	ideOptions map[string]config2.OptionValue,
	flavor vscode.Flavor,
) error {
	log.Debugf("setup %s", flavor.DisplayName())
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	log.Debugf("vscode settings: %v", vsCodeConfiguration.Settings)
	settings := ""
	if len(vsCodeConfiguration.Settings) > 0 {
		out, err := json.Marshal(vsCodeConfiguration.Settings)
		if err != nil {
			return err
		}

		settings = string(out)
	}

	user := config.GetRemoteUser(setupInfo)
	err := vscode.NewVSCodeServer(vscode.ServerOptions{
		Extensions: vsCodeConfiguration.Extensions,
		Settings:   settings,
		UserName:   user,
		Values:     ideOptions,
		Flavor:     flavor,
	}).Install()
	if err != nil {
		return err
	}

	// don't install code-server if we don't have settings or extensions
	if len(vsCodeConfiguration.Settings) == 0 && len(vsCodeConfiguration.Extensions) == 0 {
		return nil
	}

	if len(vsCodeConfiguration.Extensions) == 0 {
		return nil
	}

	return command.StartBackgroundOnce(
		fmt.Sprintf("%s-async", flavor),
		func() (*exec.Cmd, error) {
			log.Infof(
				"installing extensions in the background: %s",
				strings.Join(vsCodeConfiguration.Extensions, ","),
			)
			binaryPath, err := os.Executable()
			if err != nil {
				return nil, err
			}

			args := []string{
				cmdInternal, cmdAgent, cmdContainer, "vscode-async",
				names.Flag(names.SetupInfo), cmd.SetupInfo,
				names.Flag(names.Flavor), string(flavor),
			}

			//nolint:gosec // binaryPath is from os.Executable(), not user input
			return exec.Command(binaryPath, args...), nil
		})
}

// setupBrowserIDE installs and starts any of the browser-based IDEs
// (openvscode, code-server, VS Code Web). The per-IDE differences live in the
// browserIDEs registry, so the install/extensions/start flow is written once.
func (cmd *SetupContainerCmd) setupBrowserIDE(
	ideName string,
	setupInfo *config.Result,
	ideOptions map[string]config2.OptionValue,
) error {
	b, ok := browserIDEByName(ideName)
	if !ok {
		return fmt.Errorf("unknown browser IDE %q", ideName)
	}
	log.Debugf("setup %s", b.name)

	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	settings := ""
	if len(vsCodeConfiguration.Settings) > 0 {
		out, err := json.Marshal(vsCodeConfiguration.Settings)
		if err != nil {
			return err
		}
		settings = string(out)
	}

	server := b.newServer(browserServerSpec{
		extensions: vsCodeConfiguration.Extensions,
		settings:   settings,
		userName:   config.GetRemoteUser(setupInfo),
		host:       "0.0.0.0",
		port:       strconv.Itoa(b.defaultPort),
		values:     ideOptions,
	})

	if err := server.Install(); err != nil {
		return err
	}

	if len(vsCodeConfiguration.Extensions) > 0 {
		if err := cmd.startBrowserExtensionsInstall(b, vsCodeConfiguration.Extensions); err != nil {
			return fmt.Errorf("install extensions: %w", err)
		}
	}

	return server.Start()
}

// startBrowserExtensionsInstall launches the IDE's `<ide>-async` command as a
// background process so a slow marketplace fetch never blocks setup.
func (cmd *SetupContainerCmd) startBrowserExtensionsInstall(
	b browserIDE,
	extensions []string,
) error {
	return command.StartBackgroundOnce(b.asyncCmd, func() (*exec.Cmd, error) {
		log.Infof(
			"installing extensions in the background: %s",
			strings.Join(extensions, ","),
		)
		binaryPath, err := os.Executable()
		if err != nil {
			return nil, err
		}
		//nolint:gosec // binaryPath is from os.Executable(), not user input
		return exec.Command(
			binaryPath,
			cmdInternal,
			cmdAgent,
			cmdContainer,
			b.asyncCmd,
			names.Flag(names.SetupInfo),
			cmd.SetupInfo,
		), nil
	})
}

func configureSystemGitCredentials(
	ctx context.Context,
	client tunnel.TunnelClient,
) (func(), error) {
	if !command.Exists("git") {
		return nil, errors.New("git not found")
	}

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

	gitConfig := git.At("", git.WithStrictHostKeyChecking(false)).Config()
	scope, err := addGitCredentialHelper(ctx, gitConfig, gitCredentials)
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		log.Debug("unset setup credential helper")
		if err = gitConfig.Unset(ctx, "credential.helper", scope); err != nil {
			log.Errorf("unset credential helper %v", err)
		}
	}

	return cleanup, nil
}

// addGitCredentialHelper installs the credential helper system-wide
// (/etc/gitconfig) so it applies regardless of which local user's git
// invocation picks it up -- a container's remoteUser can differ from the
// process configuring it. Falls back to the current user's global config
// when /etc/gitconfig isn't writable (e.g. a non-root OpenShift-style pod
// running as a single fixed UID, where that multi-user concern doesn't
// apply), returning the scope actually used so the caller unsets the same one.
func addGitCredentialHelper(
	ctx context.Context,
	gitConfig *git.Config,
	value string,
) (git.ConfigScope, error) {
	err := gitConfig.Add(ctx, "credential.helper", value, git.ScopeSystem)
	if err == nil {
		return git.ScopeSystem, nil
	}
	if !isGitPermissionDenied(err) {
		return git.ConfigScope{}, fmt.Errorf("add git credential helper: %w", err)
	}

	log.Debugf("system git config is not writable, falling back to the user's global config")
	if err := gitConfig.Add(ctx, "credential.helper", value, git.ScopeGlobal); err != nil {
		return git.ConfigScope{}, fmt.Errorf("add git credential helper: %w", err)
	}
	return git.ScopeGlobal, nil
}

func isGitPermissionDenied(err error) bool {
	var cmdErr *git.CommandError
	return errors.As(err, &cmdErr) && strings.Contains(cmdErr.Stderr, "Permission denied")
}

func streamMount(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
	tunnelClient tunnel.TunnelClient,
) error {
	// if we have a platform workspace socket we connect directly to it
	if workspaceInfo.CLIOptions.Platform.Enabled {
		return streamMountFromPlatform(ctx, workspaceInfo, m)
	}

	return streamMountFromTunnel(ctx, m, tunnelClient)
}

func streamMountFromPlatform(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
) error {
	log.Infof("Download %s into DevContainer %s", m.Source, m.Target)
	req, err := buildPlatformDownloadRequest(ctx, workspaceInfo, m)
	if err != nil {
		return err
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				//nolint:gosec // pre-existing, relocated by funlen extraction; out of scope for this PR
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download workspace: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// check if the response is ok
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(
			"download workspace: body = %s, status = %s",
			string(body),
			resp.Status,
		)
	}

	progressReader := &progressReader{
		Reader: resp.Body,
	}

	if err := extract.Extract(progressReader, m.Target); err != nil {
		return fmt.Errorf("stream mount %s: %w", m.String(), err)
	}

	return nil
}

// buildPlatformDownloadRequest builds the authenticated request to the
// runner proxy socket that serves m.Source for download.
func buildPlatformDownloadRequest(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
) (*http.Request, error) {
	downloadURL := fmt.Sprintf(
		"https://%s/kubernetes/management/apis/management.devsy.sh/v1/namespaces/%s/devsyworkspaceinstances/%s/download?path=%s", //nolint:lll // pre-existing, relocated by funlen extraction; out of scope for this PR
		ts.RemoveProtocol(workspaceInfo.CLIOptions.Platform.PlatformHost),
		workspaceInfo.CLIOptions.Platform.InstanceNamespace,
		workspaceInfo.CLIOptions.Platform.InstanceName,
		url.QueryEscape(m.Source),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set(
		"Authorization",
		fmt.Sprintf("Bearer %s", workspaceInfo.CLIOptions.Platform.AccessKey),
	)

	return req, nil
}

func streamMountFromTunnel(
	ctx context.Context,
	m *config.Mount,
	tunnelClient tunnel.TunnelClient,
) error {
	log.Infof("Copy %s into DevContainer %s", m.Source, m.Target)
	stream, err := tunnelClient.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: m.String()})
	if err != nil {
		return fmt.Errorf("init stream mount %s: %w", m.String(), err)
	}

	if err := extract.Extract(tunnelserver.NewStreamReader(stream), m.Target); err != nil {
		return fmt.Errorf("stream mount %s: %w", m.String(), err)
	}

	return nil
}

type progressReader struct {
	Reader io.Reader

	lastMessage time.Time
	bytesRead   int64
}

func (p *progressReader) Read(b []byte) (n int, err error) {
	n, err = p.Reader.Read(b)
	p.bytesRead += int64(n)
	if time.Since(p.lastMessage) > time.Second*4 {
		log.Infof("downloaded %.2f MB", float64(p.bytesRead)/1024/1024)
		p.lastMessage = time.Now()
	}

	return n, err
}
