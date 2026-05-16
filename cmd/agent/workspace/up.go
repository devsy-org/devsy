package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/credentials"
	agentdaemon "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/crane"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	"github.com/devsy-org/devsy/pkg/dockerinstall"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/spf13/cobra"
)

// UpCmd holds the up cmd flags.
type UpCmd struct {
	*flags.GlobalFlags

	WorkspaceInfo string
}

// NewUpCmd creates a new command.
func NewUpCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{
		GlobalFlags: flags,
	}
	upCmd := &cobra.Command{
		Use:   "up",
		Short: "Starts a new devcontainer",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	upCmd.Flags().StringVar(&cmd.WorkspaceInfo, "workspace-info", "", "The workspace info")
	_ = upCmd.MarkFlagRequired("workspace-info")
	return upCmd
}

func (cmd *UpCmd) Run(ctx context.Context) error {
	workspaceInfo, err := cmd.loadWorkspaceInfo(ctx)
	if err != nil {
		return err
	}
	if workspaceInfo == nil {
		return nil
	}

	if cmd.shouldPreventDaemonShutdown(workspaceInfo) {
		agent.CreateWorkspaceBusyFile(workspaceInfo.Origin)
		defer agent.DeleteWorkspaceBusyFile(workspaceInfo.Origin)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	tunnelClient, credentialsDir, err := initWorkspace(initWorkspaceParams{
		ctx:                 cancelCtx,
		workspaceInfo:       workspaceInfo,
		debug:               cmd.Debug,
		shouldInstallDaemon: cmd.shouldInstallDaemon(workspaceInfo),
	})
	defer cmd.cleanupCredentials(credentialsDir)
	if err != nil {
		return cmd.handleInitError(err, workspaceInfo)
	}

	if err := cmd.up(ctx, workspaceInfo, tunnelClient); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}

	return nil
}

func (cmd *UpCmd) loadWorkspaceInfo(ctx context.Context) (*provider.AgentWorkspaceInfo, error) {
	shouldExit, workspaceInfo, err := agent.WriteWorkspaceInfoAndDeleteOld(
		cmd.WorkspaceInfo,
		func(workspaceInfo *provider.AgentWorkspaceInfo) error {
			return deleteWorkspace(ctx, workspaceInfo)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error parsing workspace info: %w", err)
	}
	if shouldExit {
		return nil, nil
	}
	return workspaceInfo, nil
}

func (cmd *UpCmd) shouldPreventDaemonShutdown(workspaceInfo *provider.AgentWorkspaceInfo) bool {
	return !workspaceInfo.CLIOptions.Platform.Enabled
}

func (cmd *UpCmd) shouldInstallDaemon(workspaceInfo *provider.AgentWorkspaceInfo) bool {
	return !workspaceInfo.CLIOptions.Platform.Enabled && !workspaceInfo.CLIOptions.DisableDaemon
}

func (cmd *UpCmd) handleInitError(
	err error,
	workspaceInfo *provider.AgentWorkspaceInfo,
) error {
	deleteErr := clientimplementation.DeleteWorkspaceFolder(
		clientimplementation.DeleteWorkspaceFolderParams{
			Context:              workspaceInfo.Workspace.Context,
			WorkspaceID:          workspaceInfo.Workspace.ID,
			SSHConfigPath:        workspaceInfo.Workspace.SSHConfigPath,
			SSHConfigIncludePath: workspaceInfo.Workspace.SSHConfigIncludePath,
		},
	)
	if deleteErr != nil {
		return fmt.Errorf("%s: %w", deleteErr.Error(), err)
	}
	return err
}

func (cmd *UpCmd) cleanupCredentials(credentialsDir string) {
	if credentialsDir != "" {
		_ = os.RemoveAll(credentialsDir)
	}
}

func (cmd *UpCmd) up(
	ctx context.Context,
	workspaceInfo *provider.AgentWorkspaceInfo,
	tunnelClient tunnel.TunnelClient,
) error {
	result, err := cmd.devsyUp(ctx, workspaceInfo)
	if err != nil {
		return err
	}

	return cmd.sendResult(ctx, result, tunnelClient)
}

func (cmd *UpCmd) sendResult(
	ctx context.Context,
	result *config2.Result,
	tunnelClient tunnel.TunnelClient,
) error {
	out, err := json.Marshal(result)
	if err != nil {
		return err
	}

	_, err = tunnelClient.SendResult(ctx, &tunnel.Message{Message: string(out)})
	if err != nil {
		return fmt.Errorf("send result: %w", err)
	}

	return nil
}

func (cmd *UpCmd) devsyUp(
	ctx context.Context,
	workspaceInfo *provider.AgentWorkspaceInfo,
) (*config2.Result, error) {
	runner, err := CreateRunner(workspaceInfo)
	if err != nil {
		return nil, err
	}

	return runner.Up(ctx, devcontainer.UpOptions{
		CLIOptions:    workspaceInfo.CLIOptions,
		RegistryCache: workspaceInfo.RegistryCache,
	}, workspaceInfo.InjectTimeout)
}

func CreateRunner(
	workspaceInfo *provider.AgentWorkspaceInfo,
) (devcontainer.Runner, error) {
	return devcontainer.NewRunner(
		agent.ContainerDevsyHelperLocation,
		agent.DefaultAgentDownloadURL(),
		workspaceInfo,
	)
}

func InitContentFolder(
	workspaceInfo *provider.AgentWorkspaceInfo,
) (bool, error) {
	exists, err := contentFolderExists(workspaceInfo.ContentFolder)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}

	if err := createContentFolder(workspaceInfo.ContentFolder); err != nil {
		return false, err
	}

	if err := downloadWorkspaceBinaries(workspaceInfo); err != nil {
		_ = os.RemoveAll(workspaceInfo.ContentFolder)
		return false, err
	}

	if workspaceInfo.LastDevContainerConfig != nil {
		if err := ensureLastDevContainerJson(workspaceInfo); err != nil {
			log.Errorf("ensure devcontainer.json: %v", err)
		}
		return true, nil
	}

	return false, nil
}

func contentFolderExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		log.Debugf("workspace folder already exists: path=%s", path)
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func createContentFolder(path string) error {
	log.Debugf("create content folder: path=%s", path)
	if err := os.MkdirAll(path, 0o750); err != nil {
		return fmt.Errorf("make workspace folder: %w", err)
	}
	return nil
}

func downloadWorkspaceBinaries(
	workspaceInfo *provider.AgentWorkspaceInfo,
) error {
	binariesDir, err := agent.GetAgentBinariesDir(
		workspaceInfo.Agent.DataPath,
		workspaceInfo.Workspace.Context,
		workspaceInfo.Workspace.ID,
	)
	if err != nil {
		return fmt.Errorf(
			"error getting workspace %s binaries dir: %w",
			workspaceInfo.Workspace.ID,
			err,
		)
	}

	_, err = provider.DownloadBinaries(workspaceInfo.Agent.Binaries, binariesDir)
	if err != nil {
		return fmt.Errorf(
			"error downloading workspace %s binaries: %w",
			workspaceInfo.Workspace.ID,
			err,
		)
	}

	return nil
}

type workspaceInitializer struct {
	ctx                  context.Context
	workspaceInfo        *provider.AgentWorkspaceInfo
	debug                bool
	shouldInstallDaemon  bool
	tunnelClient         tunnel.TunnelClient
	logger               tunnelserver.Logger
	dockerCredentialsDir string
	gitCredentialsHelper string
}

type initWorkspaceParams struct {
	ctx                 context.Context
	workspaceInfo       *provider.AgentWorkspaceInfo
	debug               bool
	shouldInstallDaemon bool
}

func initWorkspace(params initWorkspaceParams) (tunnel.TunnelClient, string, error) {
	init := &workspaceInitializer{
		ctx:                 params.ctx,
		workspaceInfo:       params.workspaceInfo,
		debug:               params.debug,
		shouldInstallDaemon: params.shouldInstallDaemon,
	}

	if err := init.initialize(); err != nil {
		return nil, init.dockerCredentialsDir, err
	}

	return init.tunnelClient, init.dockerCredentialsDir, nil
}

func (w *workspaceInitializer) initialize() error {
	if err := w.initializeTunnel(); err != nil {
		return err
	}

	if err := w.setupCredentials(); err != nil {
		log.Warnf("failed to set up docker/git credentials (continuing without them): %v", err)
	}

	dockerErrChan := w.installDockerAsync()

	if err := w.prepareWorkspaceContent(); err != nil {
		return err
	}

	w.setupDaemonIfNeeded()

	if err := w.waitForDocker(dockerErrChan); err != nil {
		return err
	}

	w.tryConfigureDockerDaemon()
	return nil
}

func (w *workspaceInitializer) setupDaemonIfNeeded() {
	if w.shouldInstallDaemon {
		if err := installDaemon(w.workspaceInfo); err != nil {
			log.Errorf("install Devsy daemon: %v", err)
		}
	}
}

func (w *workspaceInitializer) tryConfigureDockerDaemon() {
	if !w.shouldConfigureDockerDaemon() {
		log.Debug("skipping configuring docker daemon")
		return
	}
	if err := configureDockerDaemon(w.ctx); err != nil {
		log.Warn(
			"could not find docker daemon config file, if using the registry cache, " +
				"please ensure the daemon is configured with containerd-snapshotter=true, " +
				"more info at https://docs.docker.com/engine/storage/containerd/",
		)
	}
}

func (w *workspaceInitializer) initializeTunnel() error {
	client, err := tunnelserver.NewTunnelClient(os.Stdin, os.Stdout, true, 0)
	if err != nil {
		return fmt.Errorf("error creating tunnel client: %w", err)
	}
	w.tunnelClient = client
	w.logger = tunnelserver.NewTunnelLogger(w.ctx, w.tunnelClient, w.debug)
	log.Debugf("created logger")

	if _, err := w.tunnelClient.Ping(w.ctx, &tunnel.Empty{}); err != nil {
		return fmt.Errorf("ping client: %w", err)
	}

	return nil
}

func (w *workspaceInitializer) setupCredentials() error {
	dockerCredentialsDir, gitCredentialsHelper, err := configureCredentials(credentialsConfig{
		ctx:           w.ctx,
		workspaceInfo: w.workspaceInfo,
		client:        w.tunnelClient,
	})
	w.dockerCredentialsDir = dockerCredentialsDir
	w.gitCredentialsHelper = gitCredentialsHelper
	return err
}

type dockerInstallResult struct {
	path string
	err  error
}

func (w *workspaceInitializer) installDockerAsync() <-chan dockerInstallResult {
	resultChan := make(chan dockerInstallResult, 1)

	go func() {
		if !w.workspaceInfo.Agent.IsDockerDriver() {
			log.Debug("not a docker driver, skipping docker installation")
			resultChan <- dockerInstallResult{}
			return
		}

		dockerPath, err := w.ensureDockerInstalled()
		resultChan <- dockerInstallResult{path: dockerPath, err: err}
	}()

	return resultChan
}

func (w *workspaceInitializer) ensureDockerInstalled() (string, error) {
	dockerCmd := w.getDockerCommand()

	if command.Exists(dockerCmd) {
		log.Debug("docker command exists, skipping installation")
		return "", nil
	}

	if dockerCmd != "docker" {
		path, err := exec.LookPath(dockerCmd)
		if err != nil {
			return "", fmt.Errorf("custom docker path %q not found: %w", dockerCmd, err)
		}
		return path, nil
	}

	if dockerCmd == "docker" && runtime.GOOS == "darwin" {
		return findDarwinDocker()
	}

	if w.isDockerInstallDisabled() {
		log.Debug(
			"docker not found but installation was disabled, installing anyway as it is required",
		)
	}

	log.Debug("attempting to install docker")
	dockerPath, err := installDocker()
	log.Debugf("docker installation path=%q, err=%v", dockerPath, err)
	return dockerPath, err
}

// darwinDockerPaths are well-known locations where Docker Desktop installs
// the docker CLI on macOS. Declared as a variable so tests can override.
var darwinDockerPaths = []string{
	"/usr/local/bin/docker",
	"/opt/homebrew/bin/docker",
	"/Applications/Docker.app/Contents/Resources/bin/docker",
}

// findDarwinDocker checks well-known macOS Docker Desktop paths and returns
// the first one that exists. If none are found it returns an error directing
// the user to install Docker Desktop.
func findDarwinDocker() (string, error) {
	for _, path := range darwinDockerPaths {
		if _, err := os.Stat(path); err == nil {
			log.Debugf("found docker at %s", path)
			return path, nil
		}
	}
	return "", fmt.Errorf(
		"docker Desktop not found; on macOS, install Docker Desktop from https://www.docker.com/products/docker-desktop",
	)
}

func (w *workspaceInitializer) getDockerCommand() string {
	if w.workspaceInfo.Agent.Docker.Path != "" {
		log.Debugf("using custom docker path %s", w.workspaceInfo.Agent.Docker.Path)
		return w.workspaceInfo.Agent.Docker.Path
	}
	return "docker"
}

func (w *workspaceInitializer) isDockerInstallDisabled() bool {
	install, err := w.workspaceInfo.Agent.Docker.Install.Bool()
	return err == nil && !install
}

func (w *workspaceInitializer) prepareWorkspaceContent() error {
	return prepareWorkspace(prepareWorkspaceParams{
		ctx:           w.ctx,
		workspaceInfo: w.workspaceInfo,
		client:        w.tunnelClient,
		gitHelper:     w.gitCredentialsHelper,
		logger:        w.logger,
	})
}

// waitForDocker waits for the Docker installation to complete.
// Note: This function modifies workspaceInfo.Agent.Docker.Path if Docker was installed.
func (w *workspaceInitializer) waitForDocker(resultChan <-chan dockerInstallResult) error {
	result := <-resultChan

	if result.path != "" && w.workspaceInfo.Agent.Docker.Path == "" {
		w.workspaceInfo.Agent.Docker.Path = result.path
		log.Debugf("set docker path to %s", result.path)
	}

	if result.err != nil {
		return fmt.Errorf("install docker: %w", result.err)
	}

	return nil
}

func (w *workspaceInitializer) shouldConfigureDockerDaemon() bool {
	if !w.workspaceInfo.Agent.IsDockerDriver() {
		return false
	}

	local, err := w.workspaceInfo.Agent.Local.Bool()
	if err != nil {
		log.Debugf("failed to parse Local option: %v", err)
		return false
	}
	return !local
}

type prepareWorkspaceParams struct {
	ctx           context.Context
	workspaceInfo *provider.AgentWorkspaceInfo
	client        tunnel.TunnelClient
	gitHelper     string
	logger        tunnelserver.Logger
}

// prepareWorkspace initializes the workspace content folder and downloads/prepares the workspace source.
// Note: This function modifies params.workspaceInfo.ContentFolder when platform is enabled with a local folder.
func prepareWorkspace(params prepareWorkspaceParams) error {
	if params.workspaceInfo.CLIOptions.Platform.Enabled &&
		params.workspaceInfo.Workspace.Source.LocalFolder != "" {
		params.workspaceInfo.ContentFolder = agent.GetAgentWorkspaceContentDir(
			params.workspaceInfo.Origin,
		)
	}

	exists, err := InitContentFolder(params.workspaceInfo)
	if err != nil {
		return err
	}
	if exists && !params.workspaceInfo.CLIOptions.Recreate {
		params.logger.Debugf("workspace exists, skip downloading")
		return nil
	}

	if params.workspaceInfo.Workspace.Source.GitRepository != "" {
		return prepareGitWorkspace(prepareGitWorkspaceParams{
			ctx:           params.ctx,
			workspaceInfo: params.workspaceInfo,
			gitHelper:     params.gitHelper,
			exists:        exists,
			logger:        params.logger,
		})
	}

	if params.workspaceInfo.Workspace.Source.LocalFolder != "" {
		return prepareLocalWorkspace(params.ctx, params.workspaceInfo, params.client)
	}

	if params.workspaceInfo.Workspace.Source.Image != "" {
		params.logger.Debugf("prepare image")
		return prepareImage(
			params.workspaceInfo.ContentFolder,
			params.workspaceInfo.Workspace.Source.Image,
		)
	}

	if params.workspaceInfo.Workspace.Source.Container != "" {
		params.logger.Debugf("workspace is a container, nothing to do")
		return nil
	}

	return fmt.Errorf("either workspace repository, image, container or local-folder is required")
}

type prepareGitWorkspaceParams struct {
	ctx           context.Context
	workspaceInfo *provider.AgentWorkspaceInfo
	gitHelper     string
	exists        bool
	logger        tunnelserver.Logger
}

func prepareGitWorkspace(params prepareGitWorkspaceParams) error {
	if params.workspaceInfo.CLIOptions.Reset {
		params.logger.Info("resetting git based workspace, removing old content folder")
		if err := os.RemoveAll(params.workspaceInfo.ContentFolder); err != nil {
			params.logger.Warnf("failed to remove workspace folder, still proceeding: %v", err)
		}
	}

	if params.workspaceInfo.CLIOptions.Recreate && !params.workspaceInfo.CLIOptions.Reset &&
		params.exists {
		params.logger.Info(
			"rebuilding without resetting a git based workspace, keeping old content folder",
		)
		return nil
	}

	if crane.ShouldUse(&params.workspaceInfo.CLIOptions) {
		params.logger.Infof(
			"pulling devcontainer spec from %v",
			params.workspaceInfo.CLIOptions.Platform.EnvironmentTemplate,
		)
		return nil
	}

	return agent.CloneRepositoryForWorkspace(
		params.ctx,
		&params.workspaceInfo.Workspace.Source,
		&params.workspaceInfo.Agent,
		params.workspaceInfo.ContentFolder,
		params.gitHelper,
		params.workspaceInfo.CLIOptions,
		false,
	)
}

func prepareLocalWorkspace(
	ctx context.Context,
	workspaceInfo *provider.AgentWorkspaceInfo,
	client tunnel.TunnelClient,
) error {
	if workspaceInfo.ContentFolder == workspaceInfo.Workspace.Source.LocalFolder {
		log.Debugf(
			"local folder %s with local provider; skip downloading",
			workspaceInfo.ContentFolder,
		)
		return nil
	}

	log.Debugf("download local folder %s", workspaceInfo.ContentFolder)
	return downloadLocalFolder(ctx, workspaceInfo.ContentFolder, client)
}

func ensureLastDevContainerJson(workspaceInfo *provider.AgentWorkspaceInfo) error {
	filePath := filepath.Join(
		workspaceInfo.ContentFolder,
		filepath.FromSlash(workspaceInfo.LastDevContainerConfig.Path),
	)

	if _, err := os.Stat(filePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error stating %s: %w", filePath, err)
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(filePath), err)
	}

	raw, err := json.Marshal(workspaceInfo.LastDevContainerConfig.Config)
	if err != nil {
		return fmt.Errorf("marshal devcontainer.json: %w", err)
	}

	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filePath, err)
	}

	return nil
}

type credentialsConfig struct {
	ctx           context.Context
	workspaceInfo *provider.AgentWorkspaceInfo
	client        tunnel.TunnelClient
}

func configureCredentials(cfg credentialsConfig) (string, string, error) {
	if cfg.workspaceInfo.Agent.InjectDockerCredentials != config.BoolTrue &&
		cfg.workspaceInfo.Agent.InjectGitCredentials != config.BoolTrue {
		return "", "", nil
	}

	serverPort, err := credentials.StartCredentialsServer(cfg.ctx, cfg.client)
	if err != nil {
		return "", "", err
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return "", "", err
	}

	if cfg.workspaceInfo.Origin == "" {
		return "", "", fmt.Errorf("workspace folder is not set")
	}

	dockerCredentials := ""
	if cfg.workspaceInfo.Agent.InjectDockerCredentials == config.BoolTrue {
		dockerCredentials, err = dockercredentials.ConfigureCredentialsMachine(
			cfg.workspaceInfo.Origin,
			serverPort,
		)
		if err != nil {
			return "", "", err
		}
	}

	gitCredentials := ""
	if cfg.workspaceInfo.Agent.InjectGitCredentials == config.BoolTrue {
		gitCredentials = fmt.Sprintf(
			"!'%s' agent git-credentials --port %d",
			binaryPath,
			serverPort,
		)
		_ = os.Setenv(config.EnvGitHelperPort, strconv.Itoa(serverPort))
	}

	return dockerCredentials, gitCredentials, nil
}

func installDaemon(workspaceInfo *provider.AgentWorkspaceInfo) error {
	if len(workspaceInfo.Agent.Exec.Shutdown) == 0 {
		return nil
	}

	var shutdownAction string
	if workspaceInfo.LastDevContainerConfig != nil &&
		workspaceInfo.LastDevContainerConfig.Config != nil {
		shutdownAction = workspaceInfo.LastDevContainerConfig.Config.ShutdownAction
	}

	log.Debugf("installing Devsy daemon into server")
	return agentdaemon.InstallDaemon(
		workspaceInfo.Agent.DataPath,
		workspaceInfo.CLIOptions.DaemonInterval,
		shutdownAction,
	)
}

func downloadLocalFolder(
	ctx context.Context,
	workspaceDir string,
	client tunnel.TunnelClient,
) error {
	log.Infof("Upload folder to server")
	stream, err := client.StreamWorkspace(ctx, &tunnel.Empty{})
	if err != nil {
		return fmt.Errorf("read workspace: %w", err)
	}

	return extract.Extract(tunnelserver.NewStreamReader(stream), workspaceDir)
}

func prepareImage(workspaceDir, image string) error {
	devcontainerConfig := []byte(`{
  "image": "` + image + `"
}`)
	return os.WriteFile(
		filepath.Join(workspaceDir, ".devcontainer.json"),
		devcontainerConfig,
		0o600,
	)
}

// installDocker installs Docker and returns the path to the docker binary.
// This function assumes docker does not already exist - the caller should check first.
func installDocker() (dockerPath string, err error) {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	log.Debug("installing Docker")
	return dockerinstall.Install(writer, writer)
}

func configureDockerDaemon(ctx context.Context) error {
	log.Info("configuring docker daemon")

	if err := mergeDockerDaemonConfig(); err != nil {
		return err
	}

	return reloadDockerDaemon(ctx)
}

func mergeDockerDaemonConfig() error {
	rootlessErr := tryMergeRootlessDockerConfig()
	if rootlessErr == nil {
		return nil
	}

	rootErr := tryMergeRootDockerConfig()
	if rootErr == nil {
		return nil
	}

	return fmt.Errorf(
		"failed to write docker daemon config (rootless: %v, root: %v)",
		rootlessErr,
		rootErr,
	)
}

func tryMergeRootlessDockerConfig() error {
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return err
	}

	dockerConfigDir := filepath.Join(homeDir, ".config", "docker")
	if _, err := os.Stat(dockerConfigDir); errors.Is(err, os.ErrNotExist) {
		return err
	}

	configPath := filepath.Join(dockerConfigDir, "daemon.json")
	return mergeContainerdSnapshotterConfig(configPath)
}

func tryMergeRootDockerConfig() error {
	return mergeContainerdSnapshotterConfig("/etc/docker/daemon.json")
}

func mergeContainerdSnapshotterConfig(configPath string) error {
	existingConfig, err := readExistingConfig(configPath)
	if err != nil {
		return err
	}

	features := ensureFeaturesMap(existingConfig)
	features["containerd-snapshotter"] = true

	return writeConfig(configPath, existingConfig)
}

func readExistingConfig(configPath string) (map[string]any, error) {
	existingConfig := make(map[string]any)
	// #nosec G304 -- configPath is controlled by the application
	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read existing config: %w", err)
	}

	if len(data) > 0 {
		if err := json.Unmarshal(data, &existingConfig); err != nil {
			return nil, fmt.Errorf("parse existing config: %w", err)
		}
	}
	return existingConfig, nil
}

func ensureFeaturesMap(config map[string]any) map[string]any {
	features, ok := config["features"].(map[string]any)
	if !ok {
		features = make(map[string]any)
		config["features"] = features
	}
	return features
}

func writeConfig(configPath string, config map[string]any) error {
	mergedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// #nosec G301 -- directory needs to be accessible by docker daemon
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// #nosec G306 -- daemon.json needs to be readable by docker daemon
	if err := os.WriteFile(configPath, mergedData, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

func reloadDockerDaemon(ctx context.Context) error {
	err := exec.CommandContext(ctx, "pkill", "-HUP", "dockerd").Run()
	if err != nil {
		// pkill returns exit code 1 if no processes matched
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil // No dockerd process found, nothing to reload
		}
		return err
	}
	return nil
}
