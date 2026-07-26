package clientimplementation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/options"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/gofrs/flock"
)

func NewWorkspaceClient(
	devsyConfig *config.Config,
	prov *provider.ProviderConfig,
	workspace *provider.Workspace,
	machine *provider.Machine,
) (client.WorkspaceClient, error) {
	if workspace.Machine.ID != "" && machine == nil {
		return nil, errors.New("machine not found")
	}
	if prov.IsMachineProvider() && workspace.Machine.ID == "" {
		return nil, errors.New("machine id empty for machine provider")
	}

	return &workspaceClient{
		devsyConfig: devsyConfig,
		config:      prov,
		workspace:   workspace,
		machine:     machine,
	}, nil
}

type workspaceClient struct {
	m sync.Mutex

	workspaceLockOnce sync.Once
	workspaceLockErr  error
	workspaceLock     *flock.Flock
	machineLock       *flock.Flock

	devsyConfig *config.Config
	config      *provider.ProviderConfig
	workspace   *provider.Workspace
	machine     *provider.Machine
}

func (s *workspaceClient) Provider() string {
	return s.config.Name
}

func (s *workspaceClient) Context() string {
	return s.workspace.Context
}

func (s *workspaceClient) Workspace() string {
	s.m.Lock()
	defer s.m.Unlock()

	return s.workspace.ID
}

func (s *workspaceClient) WorkspaceConfig() *provider.Workspace {
	s.m.Lock()
	defer s.m.Unlock()

	return provider.CloneWorkspace(s.workspace)
}

func (s *workspaceClient) AgentLocal() bool {
	s.m.Lock()
	defer s.m.Unlock()

	return s.agentLocal()
}

func (s *workspaceClient) AgentPath() string {
	s.m.Lock()
	defer s.m.Unlock()

	return s.agentConfig().Path
}

func (s *workspaceClient) AgentURL() string {
	s.m.Lock()
	defer s.m.Unlock()

	return s.agentConfig().DownloadURL
}

func (s *workspaceClient) RefreshOptions(
	ctx context.Context,
	userOptionsRaw []string,
	reconfigure bool,
) error {
	s.m.Lock()
	defer s.m.Unlock()

	userOptions, err := provider.ParseOptions(userOptionsRaw)
	if err != nil {
		return fmt.Errorf("parse options: %w", err)
	}

	if s.isMachineProvider() {
		return s.refreshMachineOptions(ctx, userOptions)
	}
	return s.refreshWorkspaceOptions(ctx, userOptions)
}

func (s *workspaceClient) AgentInjectGitCredentials(cliOptions provider.CLIOptions) bool {
	s.m.Lock()
	defer s.m.Unlock()

	return s.agentInfo(cliOptions).Agent.InjectGitCredentials == config.BoolTrue
}

func (s *workspaceClient) AgentInjectDockerCredentials(cliOptions provider.CLIOptions) bool {
	s.m.Lock()
	defer s.m.Unlock()

	return s.agentInfo(cliOptions).Agent.InjectDockerCredentials == config.BoolTrue
}

func (s *workspaceClient) AgentInfo(
	cliOptions provider.CLIOptions,
) (string, *provider.AgentWorkspaceInfo, error) {
	s.m.Lock()
	defer s.m.Unlock()

	return s.compressedAgentInfo(cliOptions)
}

func (s *workspaceClient) Lock(ctx context.Context) error {
	if err := s.initLock(); err != nil {
		return fmt.Errorf("init lock: %w", err)
	}

	log.Debug("acquire workspace lock")
	if err := tryLock(ctx, s.workspaceLock, "workspace"); err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	log.Debug("acquired workspace lock")

	if s.machineLock != nil {
		log.Debug("acquire machine lock")
		if err := tryLock(ctx, s.machineLock, "machine"); err != nil {
			return fmt.Errorf("lock machine: %w", err)
		}
		log.Debug("acquired machine lock")
	}

	return nil
}

func (s *workspaceClient) Unlock() {
	if err := s.initLock(); err != nil {
		log.Warnf("init lock: %v", err)
		return
	}

	if s.machineLock != nil {
		if err := s.machineLock.Unlock(); err != nil {
			log.Warnf("unlock machine: %v", err)
		}
	}

	if s.workspaceLock != nil {
		if err := s.workspaceLock.Unlock(); err != nil {
			log.Warnf("unlock workspace: %v", err)
		}
	}
}

func (s *workspaceClient) Create(ctx context.Context) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() {
		return nil
	}
	if s.machine == nil {
		return errors.New("machine not defined")
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		return err
	}

	status, err := machineClient.Status(ctx, client.StatusOptions{})
	if err != nil {
		return err
	}
	if status != client.StatusNotFound {
		return nil
	}

	return machineClient.Create(ctx)
}

func (s *workspaceClient) Delete(ctx context.Context, opt client.DeleteOptions) error {
	s.m.Lock()
	defer s.m.Unlock()

	ctx, cancel := s.deleteContext(ctx, opt.GracePeriod)
	defer cancel()

	if err := s.deleteInstance(ctx, opt); err != nil {
		return err
	}

	return DeleteWorkspaceFolder(DeleteWorkspaceFolderParams{
		Context:              s.workspace.Context,
		WorkspaceID:          s.workspace.ID,
		SSHConfigPath:        s.workspace.SSHConfigPath,
		SSHConfigIncludePath: s.workspace.SSHConfigIncludePath,
	})
}

func (s *workspaceClient) Start(ctx context.Context) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() || s.machine == nil {
		return nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		return err
	}

	return machineClient.Start(ctx)
}

func (s *workspaceClient) Stop(ctx context.Context, opt client.StopOptions) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() || !s.workspace.Machine.AutoDelete {
		return s.stopContainer(ctx)
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		return err
	}

	return machineClient.Stop(ctx, opt)
}

func (s *workspaceClient) Command(ctx context.Context, opt client.CommandOptions) error {
	environ, err := s.buildEnvironment(opt.Command)
	if err != nil {
		return err
	}

	return RunCommand(RunCommandOptions{
		Ctx:     ctx,
		Command: s.config.Exec.Command,
		Environ: environ,
		Stdin:   opt.Stdin,
		Stdout:  opt.Stdout,
		Stderr:  opt.Stderr,
	})
}

func (s *workspaceClient) Status(
	ctx context.Context,
	opt client.StatusOptions,
) (client.Status, error) {
	s.m.Lock()
	defer s.m.Unlock()

	if s.isMachineProvider() && len(s.config.Exec.Status) > 0 {
		return s.machineStatus(ctx, opt)
	}

	if opt.ContainerStatus {
		return s.getContainerStatus(ctx)
	}

	return s.workspaceFolderStatus()
}

func (s *workspaceClient) Describe(ctx context.Context) (string, error) {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() || len(s.config.Exec.Describe) == 0 {
		return client.DescriptionNotFound, nil
	}
	if s.machine == nil {
		return client.DescriptionNotFound, nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		return client.DescriptionNotFound, err
	}

	return machineClient.Describe(ctx)
}

func (s *workspaceClient) agentConfig() provider.ProviderAgentConfig {
	return options.ResolveAgentConfig(s.devsyConfig, s.config, s.workspace, s.machine)
}

func (s *workspaceClient) agentLocal() bool {
	return s.agentConfig().Local == config.BoolTrue
}

func (s *workspaceClient) isMachineProvider() bool {
	return len(s.config.Exec.Create) > 0
}

func (s *workspaceClient) refreshMachineOptions(
	ctx context.Context,
	userOptions map[string]string,
) error {
	if s.machine == nil {
		return nil
	}

	machine, err := options.ResolveAndSaveOptionsMachine(
		ctx,
		s.devsyConfig,
		s.config,
		s.machine,
		userOptions,
	)
	if err != nil {
		return err
	}

	s.machine = machine
	return nil
}

func (s *workspaceClient) refreshWorkspaceOptions(
	ctx context.Context,
	userOptions map[string]string,
) error {
	workspace, err := options.ResolveAndSaveOptionsWorkspace(
		ctx,
		s.devsyConfig,
		s.config,
		s.workspace,
		userOptions,
	)
	if err != nil {
		return fmt.Errorf("resolve and save workspace options: %w", err)
	}

	if workspace == nil {
		log.Debug("workspace is nil; not updating workspace options")
		return nil
	}

	s.workspace = workspace
	log.Debugf("refreshed workspace options: workspaceId=%s", s.workspace.ID)
	return nil
}

func (s *workspaceClient) compressedAgentInfo(
	cliOptions provider.CLIOptions,
) (string, *provider.AgentWorkspaceInfo, error) {
	agentInfo := s.agentInfo(cliOptions)

	out, err := json.Marshal(agentInfo)
	if err != nil {
		return "", nil, err
	}

	compressed, err := compress.Compress(string(out))
	if err != nil {
		return "", nil, err
	}

	return compressed, agentInfo, nil
}

func (s *workspaceClient) agentInfo(cliOptions provider.CLIOptions) *provider.AgentWorkspaceInfo {
	agentInfo := &provider.AgentWorkspaceInfo{
		WorkspaceOrigin:        s.workspaceOrigin(),
		Workspace:              s.workspace,
		Machine:                s.machine,
		LastDevContainerConfig: s.lastDevContainerConfig(),
		CLIOptions:             cliOptions,
		Agent:                  s.agentConfig(),
		Options:                s.devsyConfig.ProviderOptions(s.Provider()),
		InjectTimeout: config.ParseTimeOption(
			s.devsyConfig,
			config.ContextOptionAgentInjectTimeout,
		),
		RegistryCache: s.devsyConfig.ContextOption(config.ContextOptionRegistryCache),
	}

	if cliOptions.Platform.Enabled {
		agentInfo.Agent.InjectGitCredentials = config.BoolTrue
		agentInfo.Agent.InjectDockerCredentials = config.BoolTrue
	}

	if agentInfo.Agent.Driver != provider.CustomDriver &&
		(cliOptions.Platform.Enabled || cliOptions.DisableDaemon) {
		stripProviderOptions(agentInfo)
	}

	return agentInfo
}

func (s *workspaceClient) workspaceOrigin() string {
	if s.workspace == nil {
		return ""
	}
	return s.workspace.Origin
}

func (s *workspaceClient) lastDevContainerConfig() *config2.DevContainerConfigWithPath {
	if s.workspace == nil {
		return nil
	}

	result, err := provider.LoadWorkspaceResult(s.workspace.Context, s.workspace.ID)
	if err != nil {
		log.Debugf("load workspace result: %v", err)
		return nil
	}
	if result == nil {
		return nil
	}

	return result.DevContainerConfigWithPath
}

func (s *workspaceClient) deleteContext(
	ctx context.Context,
	gracePeriod string,
) (context.Context, context.CancelFunc) {
	if gracePeriod == "" {
		return context.WithCancel(ctx)
	}

	duration, err := time.ParseDuration(gracePeriod)
	if err != nil {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, duration)
}

func (s *workspaceClient) deleteInstance(ctx context.Context, opt client.DeleteOptions) error {
	if !s.isMachineProvider() || !s.workspace.Machine.AutoDelete {
		return s.deleteContainer(ctx, opt)
	}

	if s.machine != nil && s.workspace.Machine.ID != "" && len(s.config.Exec.Delete) > 0 {
		return s.deleteMachine(ctx, opt)
	}

	return nil
}

func (s *workspaceClient) deleteContainer(ctx context.Context, opt client.DeleteOptions) error {
	isRunning, err := s.isMachineRunning(ctx)
	if err != nil {
		if opt.Force {
			return nil
		}
		return err
	}
	if !isRunning {
		return nil
	}

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	log.Info("deleting workspace container")
	command, err := s.agentWorkspaceCommand("delete")
	if err != nil {
		return err
	}
	if opt.RemoveVolumes {
		command += " --remove-volumes"
	}

	return handleForceError(s.runProviderCommand(ctx, command, writer, writer), opt.Force)
}

func handleForceError(err error, force bool) error {
	if err == nil || !force {
		return err
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		log.Errorf("delete container: %v", err)
	}
	return nil
}

func (s *workspaceClient) deleteMachine(ctx context.Context, opt client.DeleteOptions) error {
	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		if !opt.Force {
			return err
		}
		return nil
	}

	return machineClient.Delete(ctx, opt)
}

func (s *workspaceClient) stopContainer(ctx context.Context) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	log.Info("stopping container")
	command, err := s.agentWorkspaceCommand("stop")
	if err != nil {
		return err
	}

	if err := s.runProviderCommand(ctx, command, writer, writer); err != nil {
		return err
	}
	log.Info("stopped container")

	return nil
}

func (s *workspaceClient) isMachineRunning(ctx context.Context) (bool, error) {
	if !s.isMachineProvider() {
		return true, nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		return false, err
	}

	status, err := machineClient.Status(ctx, client.StatusOptions{})
	if err != nil {
		return false, fmt.Errorf("retrieve machine status: %w", err)
	}

	return status == client.StatusRunning, nil
}

func (s *workspaceClient) machineStatus(
	ctx context.Context,
	opt client.StatusOptions,
) (client.Status, error) {
	if s.machine == nil {
		return client.StatusNotFound, nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.config, s.machine)
	if err != nil {
		return client.StatusNotFound, err
	}

	status, err := machineClient.Status(ctx, opt)
	if err != nil {
		return status, err
	}

	if status == client.StatusRunning && opt.ContainerStatus {
		return s.getContainerStatus(ctx)
	}

	return status, nil
}

func (s *workspaceClient) workspaceFolderStatus() (client.Status, error) {
	workspaceFolder, err := provider.GetWorkspaceDir(s.workspace.Context, s.workspace.ID)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(workspaceFolder); err == nil {
		return client.StatusRunning, nil
	}

	return client.StatusNotFound, nil
}

func (s *workspaceClient) getContainerStatus(ctx context.Context) (client.Status, error) {
	stdout := &bytes.Buffer{}
	buf := &bytes.Buffer{}

	command, err := s.agentWorkspaceCommand("status")
	if err != nil {
		return "", err
	}

	if err := s.runProviderCommand(ctx, command, io.MultiWriter(stdout, buf), buf); err != nil {
		return client.StatusNotFound, fmt.Errorf(
			"retrieve container status: %s: %w",
			buf.String(),
			err,
		)
	}

	parsed, err := client.ParseStatus(stdout.String())
	if err != nil {
		return client.StatusNotFound, fmt.Errorf(
			"parse container status: %s: %w",
			buf.String(),
			err,
		)
	}

	log.Debugf(
		"container status: stdout=%s, stderr=%s, parsed=%v",
		stdout.String(),
		buf.String(),
		parsed,
	)
	return parsed, nil
}

func (s *workspaceClient) agentWorkspaceCommand(subcommand string) (string, error) {
	compressed, info, err := s.compressedAgentInfo(provider.CLIOptions{})
	if err != nil {
		return "", fmt.Errorf("agent info: %w", err)
	}

	return fmt.Sprintf(
		"%q internal agent workspace %s --workspace-info %q",
		info.Agent.Path,
		subcommand,
		compressed,
	), nil
}

func (s *workspaceClient) runProviderCommand(
	ctx context.Context,
	command string,
	stdout, stderr io.Writer,
) error {
	return RunCommandWithBinaries(CommandOptions{
		Ctx:       ctx,
		Command:   s.config.Exec.Command,
		Context:   s.workspace.Context,
		Workspace: s.workspace,
		Machine:   s.machine,
		Options:   s.devsyConfig.ProviderOptions(s.config.Name),
		Config:    s.config,
		ExtraEnv:  map[string]string{provider.CommandEnv: command},
		Stdout:    stdout,
		Stderr:    stderr,
	})
}

func (s *workspaceClient) buildEnvironment(command string) ([]string, error) {
	s.m.Lock()
	defer s.m.Unlock()

	return provider.ToEnvironmentWithBinaries(provider.EnvironmentOptions{
		Context:   s.workspace.Context,
		Workspace: s.workspace,
		Machine:   s.machine,
		Options:   s.devsyConfig.ProviderOptions(s.config.Name),
		Config:    s.config,
		ExtraEnv:  map[string]string{provider.CommandEnv: command},
	})
}

func (s *workspaceClient) initLock() error {
	s.workspaceLockOnce.Do(func() {
		s.m.Lock()
		defer s.m.Unlock()

		workspaceLocksDir, err := provider.GetLocksDir(s.workspace.Context)
		if err != nil {
			s.workspaceLockErr = fmt.Errorf("get workspaces dir: %w", err)
			return
		}
		if err = os.MkdirAll(workspaceLocksDir, 0o755); err != nil { // #nosec G301
			s.workspaceLockErr = fmt.Errorf("create workspace locks dir: %w", err)
			return
		}

		s.workspaceLock = flock.New(
			filepath.Join(workspaceLocksDir, s.workspace.ID+".workspace.lock"),
		)

		if s.machine != nil {
			s.machineLock = flock.New(
				filepath.Join(workspaceLocksDir, s.machine.ID+".machine.lock"),
			)
		}
	})
	return s.workspaceLockErr
}

func stripProviderOptions(info *provider.AgentWorkspaceInfo) {
	info.Options = map[string]config.OptionValue{}
	info.Workspace = provider.CloneWorkspace(info.Workspace)
	info.Workspace.Provider.Options = map[string]config.OptionValue{}

	if info.Machine != nil {
		info.Machine = provider.CloneMachine(info.Machine)
		info.Machine.Provider.Options = map[string]config.OptionValue{}
	}
}

type CommandOptions struct {
	Ctx       context.Context
	Command   types.StrArray
	Context   string
	Workspace *provider.Workspace
	Machine   *provider.Machine
	Options   map[string]config.OptionValue
	Config    *provider.ProviderConfig
	ExtraEnv  map[string]string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

func RunCommandWithBinaries(opts CommandOptions) error {
	environ, err := provider.ToEnvironmentWithBinaries(provider.EnvironmentOptions{
		Context:   opts.Context,
		Workspace: opts.Workspace,
		Machine:   opts.Machine,
		Options:   opts.Options,
		Config:    opts.Config,
		ExtraEnv:  opts.ExtraEnv,
	})
	if err != nil {
		return err
	}

	return RunCommand(RunCommandOptions{
		Ctx:     opts.Ctx,
		Command: opts.Command,
		Environ: environ,
		Stdin:   opts.Stdin,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
	})
}

type RunCommandOptions struct {
	Ctx     context.Context
	Command types.StrArray
	Environ []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func RunCommand(opts RunCommandOptions) error {
	if len(opts.Command) == 0 {
		return nil
	}

	if log.DebugEnabled() {
		opts.Environ = append(opts.Environ, config.EnvDebug+"="+config.BoolTrue)
	}

	if len(opts.Command) == 1 {
		return shell.RunEmulatedShell(
			opts.Ctx,
			opts.Command[0],
			opts.Stdin,
			opts.Stdout,
			opts.Stderr,
			opts.Environ,
		)
	}

	cmd := exec.CommandContext(opts.Ctx, opts.Command[0], opts.Command[1:]...) // #nosec G204
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	cmd.Env = opts.Environ
	return cmd.Run()
}

func DeleteMachineFolder(context, machineID string) error {
	machineDir, err := provider.GetMachineDir(context, machineID)
	if err != nil {
		return err
	}

	return removeAll(machineDir)
}

type DeleteWorkspaceFolderParams struct {
	Context              string
	WorkspaceID          string
	SSHConfigPath        string
	SSHConfigIncludePath string
}

func DeleteWorkspaceFolder(params DeleteWorkspaceFolderParams) error {
	if err := removeSSHConfig(params); err != nil {
		return err
	}

	if err := removeWorkspaceDir(params.Context, params.WorkspaceID); err != nil {
		return err
	}

	return removeWorkspaceContent(params.Context, params.WorkspaceID)
}

func removeSSHConfig(params DeleteWorkspaceFolderParams) error {
	sshConfigPath, err := ssh.ResolveSSHConfigPath(params.SSHConfigPath)
	if err != nil {
		return err
	}

	sshConfigIncludePath := params.SSHConfigIncludePath
	if sshConfigIncludePath != "" {
		sshConfigIncludePath, err = ssh.ResolveSSHConfigPath(sshConfigIncludePath)
		if err != nil {
			return err
		}
	}

	if err := ssh.RemoveFromConfig(
		params.WorkspaceID,
		sshConfigPath,
		sshConfigIncludePath,
	); err != nil {
		log.Errorf("remove workspace %q from ssh config: %v", params.WorkspaceID, err)
	}

	return nil
}

func removeWorkspaceDir(context, workspaceID string) error {
	workspaceFolder, err := provider.GetWorkspaceDir(context, workspaceID)
	if err != nil {
		return err
	}

	return removeAll(workspaceFolder)
}

func removeWorkspaceContent(context, workspaceID string) error {
	contentFolder, err := provider.GetWorkspaceContentDir(context, workspaceID)
	if err != nil {
		return nil
	}

	return removeAll(contentFolder)
}

func removeAll(path string) error {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

const (
	pollInterval = 2 * time.Second
	logThreshold = 10 * time.Second
)

// StartWait waits for the workspace to be ready, optionally creating/starting it.
func StartWait(ctx context.Context, workspaceClient client.WorkspaceClient, create bool) error {
	startWaiting := time.Now()
	for {
		done, err := startWaitStep(ctx, workspaceClient, create, &startWaiting)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func startWaitStep(
	ctx context.Context,
	workspaceClient client.WorkspaceClient,
	create bool,
	startWaiting *time.Time,
) (bool, error) {
	status, err := workspaceClient.Status(ctx, client.StatusOptions{})
	if err != nil {
		return false, err
	}

	switch status {
	case client.StatusBusy:
		logBusy(startWaiting)
		time.Sleep(pollInterval)
		return false, nil
	case client.StatusStopped:
		return false, handleStoppedStatus(ctx, workspaceClient, create)
	case client.StatusNotFound:
		return false, handleNotFoundStatus(ctx, workspaceClient, create)
	default:
		return true, nil
	}
}

func logBusy(startWaiting *time.Time) {
	if time.Since(*startWaiting) > logThreshold {
		log.Info("workspace is busy, waiting for it to become ready")
		*startWaiting = time.Now()
	}
}

func handleStoppedStatus(
	ctx context.Context,
	workspaceClient client.WorkspaceClient,
	create bool,
) error {
	if !create {
		return errors.New("workspace is stopped")
	}
	if err := workspaceClient.Start(ctx); err != nil {
		return fmt.Errorf("start workspace: %w", err)
	}
	return nil
}

func handleNotFoundStatus(
	ctx context.Context,
	workspaceClient client.WorkspaceClient,
	create bool,
) error {
	if !create {
		return errors.New("workspace not found")
	}
	return workspaceClient.Create(ctx)
}

// BuildAgentClientOptions contains parameters for BuildAgentClient.
type BuildAgentClientOptions struct {
	WorkspaceClient client.WorkspaceClient
	CLIOptions      provider.CLIOptions
	AgentCommand    string
	TunnelOptions   []tunnelserver.Option
}

// BuildAgentClient builds an agent client for workspace operations.
func BuildAgentClient(ctx context.Context, opts BuildAgentClientOptions) (*config2.Result, error) {
	workspaceInfo, wInfo, err := opts.WorkspaceClient.AgentInfo(opts.CLIOptions)
	if err != nil {
		return nil, err
	}

	pipes, err := newAgentPipes()
	if err != nil {
		return nil, err
	}
	defer pipes.close()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	command := buildAgentCommand(opts.WorkspaceClient, opts.AgentCommand, workspaceInfo)
	errChan := runAgentInjection(agentInjectionOptions{
		ctx:             cancelCtx,
		workspaceClient: opts.WorkspaceClient,
		command:         command,
		stdin:           pipes.stdinReader,
		stdout:          pipes.stdoutWriter,
		timeout:         wInfo.InjectTimeout,
		cancel:          cancel,
	})

	result, err := runTunnelServer(cancelCtx, opts, pipes.stdoutReader, pipes.stdinWriter)
	if err != nil {
		return nil, err
	}

	return result, <-errChan
}

func buildAgentCommand(
	workspaceClient client.WorkspaceClient,
	agentCommand, workspaceInfo string,
) string {
	command := fmt.Sprintf(
		"%q internal agent workspace %s --workspace-info %q",
		workspaceClient.AgentPath(),
		agentCommand,
		workspaceInfo,
	)
	if log.DebugEnabled() {
		command += " --debug"
	}
	return command
}

type agentPipes struct {
	stdoutReader *os.File
	stdoutWriter *os.File
	stdinReader  *os.File
	stdinWriter  *os.File
}

func newAgentPipes() (*agentPipes, error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return nil, err
	}

	return &agentPipes{
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
		stdinReader:  stdinReader,
		stdinWriter:  stdinWriter,
	}, nil
}

func (p *agentPipes) close() {
	_ = p.stdoutWriter.Close()
	_ = p.stdoutReader.Close()
	_ = p.stdinReader.Close()
	_ = p.stdinWriter.Close()
}

type agentInjectionOptions struct {
	ctx             context.Context
	workspaceClient client.WorkspaceClient
	command         string
	stdin           *os.File
	stdout          *os.File
	timeout         time.Duration
	cancel          context.CancelFunc
}

func runAgentInjection(opts agentInjectionOptions) chan error {
	errChan := make(chan error, 1)
	go func() {
		defer log.Debugf("up command completed")
		defer opts.cancel()

		writer := log.Writer(log.LevelInfo)
		defer func() { _ = writer.Close() }()

		errChan <- agent.InjectAgent(&agent.InjectOptions{
			Ctx: opts.ctx,
			Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
				return opts.workspaceClient.Command(ctx, client.CommandOptions{
					Command: command,
					Stdin:   stdin,
					Stdout:  stdout,
					Stderr:  stderr,
				})
			},
			IsLocal:         opts.workspaceClient.AgentLocal(),
			RemoteAgentPath: opts.workspaceClient.AgentPath(),
			DownloadURL:     opts.workspaceClient.AgentURL(),
			Command:         opts.command,
			Stdin:           opts.stdin,
			Stdout:          opts.stdout,
			Stderr:          writer,
			Timeout:         opts.timeout,
		})
	}()
	return errChan
}

func runTunnelServer(
	ctx context.Context,
	opts BuildAgentClientOptions,
	stdoutReader, stdinWriter *os.File,
) (*config2.Result, error) {
	result, err := tunnelserver.RunUpServer(
		ctx,
		stdoutReader,
		stdinWriter,
		opts.WorkspaceClient.AgentInjectGitCredentials(opts.CLIOptions),
		opts.WorkspaceClient.AgentInjectDockerCredentials(opts.CLIOptions),
		opts.WorkspaceClient.WorkspaceConfig(),
		append(opts.TunnelOptions,
			tunnelserver.WithGitToken(opts.CLIOptions.GitToken))...,
	)
	if err != nil {
		return nil, fmt.Errorf("run tunnel server: %w", err)
	}
	return result, nil
}
