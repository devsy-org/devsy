package clientimplementation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/options"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/devsy-org/devsy/pkg/types"
	"mvdan.cc/sh/v3/interp"
)

func NewWorkspaceClient(
	devsyConfig *config.Config,
	providerConfig *provider.ProviderConfig,
	workspace *provider.Workspace,
	machine *provider.Machine,
) (client.WorkspaceClient, error) {
	if workspace.Machine.ID != "" && machine == nil {
		return nil, errors.New("machine not found")
	}
	if providerConfig.IsMachineProvider() && workspace.Machine.ID == "" {
		return nil, errors.New("machine id empty for machine provider")
	}

	c := &workspaceClient{
		devsyConfig:       devsyConfig,
		providerConfig:    providerConfig,
		providerWorkspace: workspace,
		providerMachine:   machine,
	}
	c.locks = newClientLocks(c.lockIDs)
	return c, nil
}

type workspaceClient struct {
	m sync.Mutex

	locks *clientLocks

	devsyConfig       *config.Config
	providerConfig    *provider.ProviderConfig
	providerWorkspace *provider.Workspace
	providerMachine   *provider.Machine
}

func (s *workspaceClient) Provider() string {
	return s.providerConfig.Name
}

func (s *workspaceClient) Context() string {
	return s.providerWorkspace.Context
}

func (s *workspaceClient) Workspace() string {
	s.m.Lock()
	defer s.m.Unlock()

	return s.providerWorkspace.ID
}

func (s *workspaceClient) WorkspaceConfig() *provider.Workspace {
	s.m.Lock()
	defer s.m.Unlock()

	return provider.CloneWorkspace(s.providerWorkspace)
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
	return s.locks.Lock(ctx)
}

func (s *workspaceClient) Unlock() {
	s.locks.Unlock()
}

func (s *workspaceClient) Create(ctx context.Context) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() {
		return nil
	}
	if s.providerMachine == nil {
		return errors.New("machine not defined")
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
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
		Context:              s.providerWorkspace.Context,
		WorkspaceID:          s.providerWorkspace.ID,
		SSHConfigPath:        s.providerWorkspace.SSHConfigPath,
		SSHConfigIncludePath: s.providerWorkspace.SSHConfigIncludePath,
	})
}

func (s *workspaceClient) Start(ctx context.Context) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() || s.providerMachine == nil {
		return nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
	if err != nil {
		return err
	}

	return machineClient.Start(ctx)
}

func (s *workspaceClient) Stop(ctx context.Context, opt client.StopOptions) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() || !s.providerWorkspace.Machine.AutoDelete {
		return s.stopContainer(ctx)
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
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

	return RunCommand(ctx, RunCommandOptions{
		Command:      s.providerConfig.Exec.Command,
		Environ:      environ,
		Stdin:        opt.Stdin,
		Stdout:       opt.Stdout,
		Stderr:       opt.Stderr,
		RawStdout:    opt.RawStdout,
		Cancellation: s.providerConfig.Exec.Cancellation,
	})
}

func (s *workspaceClient) OpenCommandTransport(
	ctx context.Context,
	opt client.CommandTransportOptions,
) (transport.ManagedConn, error) {
	environ, err := s.buildEnvironment(opt.Command)
	if err != nil {
		return nil, err
	}
	return transport.OpenCallbackConn(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return RunCommand(ctx, RunCommandOptions{
				Command:          s.providerConfig.Exec.Command,
				Environ:          environ,
				Stdin:            stdin,
				Stdout:           stdout,
				Stderr:           opt.Stderr,
				RawStdout:        true,
				Cancellation:     s.providerConfig.Exec.Cancellation,
				closeInputOnExit: stdin.(io.Closer),
			})
		},
		transport.CallbackConnOptions{
			LocalAddr:  transport.NewAddr("workspace"),
			RemoteAddr: transport.NewAddr("provider:" + s.Provider()),
		},
	)
}

func (s *workspaceClient) Status(
	ctx context.Context,
	opt client.StatusOptions,
) (client.Status, error) {
	s.m.Lock()
	defer s.m.Unlock()

	var (
		result client.Status
		err    error
	)
	switch {
	case s.isMachineProvider() && len(s.providerConfig.Exec.Status) > 0:
		result, err = s.machineStatus(ctx, opt)
	case opt.ContainerStatus:
		result, err = s.getContainerStatus(ctx)
	default:
		result, err = s.workspaceFolderStatus()
	}
	if err != nil || result != client.StatusNotFound {
		return result, err
	}

	if override, ok := s.taskStatusOverride(); ok {
		return override, nil
	}
	return result, nil
}

func (s *workspaceClient) Describe(ctx context.Context) (string, error) {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.isMachineProvider() || len(s.providerConfig.Exec.Describe) == 0 {
		return client.DescriptionNotFound, nil
	}
	if s.providerMachine == nil {
		return client.DescriptionNotFound, nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
	if err != nil {
		return client.DescriptionNotFound, err
	}

	return machineClient.Describe(ctx)
}

// taskStatusOverride reports Provisioning or Failed based on the most
// recently started `up` task for this workspace.
func (s *workspaceClient) taskStatusOverride() (client.Status, bool) {
	latest := s.latestUpTask()
	if latest == nil {
		return "", false
	}

	switch {
	case !latest.Status.Terminal():
		return client.StatusProvisioning, true
	case latest.Status == task.StatusFailed && !latest.Canceled():
		return client.StatusFailed, true
	default:
		return "", false
	}
}

// latestUpTask returns the most recently started `up` task recorded for this
// workspace, or nil if none exists or task state can't be read.
func (s *workspaceClient) latestUpTask() *task.State {
	store, err := task.NewStore()
	if err != nil {
		return nil
	}
	states, err := store.List()
	if err != nil {
		return nil
	}

	latest := newestUpTask(states, s.providerWorkspace.ID)
	if latest == nil {
		return nil
	}
	return store.ReconcileState(latest)
}

// newestUpTask picks the most recently started `up` task for workspaceID.
func newestUpTask(states []*task.State, workspaceID string) *task.State {
	var latest *task.State
	for _, st := range states {
		if st.WorkspaceID != workspaceID || st.Command != "up" {
			continue
		}
		if latest == nil || st.StartedAt.After(latest.StartedAt) {
			latest = st
		}
	}
	return latest
}

func (s *workspaceClient) agentConfig() provider.ProviderAgentConfig {
	return options.ResolveAgentConfig(
		s.devsyConfig,
		s.providerConfig,
		s.providerWorkspace,
		s.providerMachine,
	)
}

func (s *workspaceClient) agentLocal() bool {
	return s.agentConfig().Local == config.BoolTrue
}

func (s *workspaceClient) isMachineProvider() bool {
	return len(s.providerConfig.Exec.Create) > 0
}

func (s *workspaceClient) refreshMachineOptions(
	ctx context.Context,
	userOptions map[string]string,
) error {
	if s.providerMachine == nil {
		return nil
	}

	machine, err := options.ResolveAndSaveOptionsMachine(ctx, options.ResolveMachineRequest{
		DevConfig:       s.devsyConfig,
		ProviderConfig:  s.providerConfig,
		OriginalMachine: s.providerMachine,
		UserOptions:     userOptions,
	})
	if err != nil {
		return err
	}

	s.providerMachine = machine
	return nil
}

func (s *workspaceClient) refreshWorkspaceOptions(
	ctx context.Context,
	userOptions map[string]string,
) error {
	workspace, err := options.ResolveAndSaveOptionsWorkspace(ctx, options.ResolveWorkspaceRequest{
		DevConfig:         s.devsyConfig,
		ProviderConfig:    s.providerConfig,
		OriginalWorkspace: s.providerWorkspace,
		UserOptions:       userOptions,
	})
	if err != nil {
		return fmt.Errorf("resolve and save workspace options: %w", err)
	}

	if workspace == nil {
		log.Debug("workspace is nil; not updating workspace options")
		return nil
	}

	s.providerWorkspace = workspace
	log.Debugf("refreshed workspace options: workspaceId=%s", s.providerWorkspace.ID)
	return nil
}

func (s *workspaceClient) compressedAgentInfo(
	cliOptions provider.CLIOptions,
) (string, *provider.AgentWorkspaceInfo, error) {
	agentInfo := s.agentInfo(cliOptions)
	if agentInfo.Agent.Driver == provider.ExternalDriver {
		if err := provider.ValidateExternalDriverConfig(agentInfo.Agent); err != nil {
			return "", nil, fmt.Errorf("validate external runtime configuration: %w", err)
		}
	}

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
		Workspace:              s.providerWorkspace,
		Machine:                s.providerMachine,
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
	if s.providerWorkspace == nil {
		return ""
	}
	return s.providerWorkspace.Origin
}

func (s *workspaceClient) lastDevContainerConfig() *config2.DevContainerConfigWithPath {
	if s.providerWorkspace == nil {
		return nil
	}

	result, err := provider.LoadWorkspaceResult(s.providerWorkspace.Context, s.providerWorkspace.ID)
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
	if !s.isMachineProvider() || !s.providerWorkspace.Machine.AutoDelete {
		return s.deleteContainer(ctx, opt)
	}

	if s.providerMachine != nil && s.providerWorkspace.Machine.ID != "" &&
		len(s.providerConfig.Exec.Delete) > 0 {
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

	writer, done := log.PipeJSONStream()
	defer func() {
		_ = writer.Close()
		<-done
	}()

	log.Debug("deleting workspace container")
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
	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
	if err != nil {
		if !opt.Force {
			return err
		}
		return nil
	}

	return machineClient.Delete(ctx, opt)
}

func (s *workspaceClient) stopContainer(ctx context.Context) error {
	writer, done := log.PipeJSONStream()
	defer func() {
		_ = writer.Close()
		<-done
	}()

	log.Debug("stopping container")
	command, err := s.agentWorkspaceCommand("stop")
	if err != nil {
		return err
	}

	if err := s.runProviderCommand(ctx, command, writer, writer); err != nil {
		return err
	}
	log.Debug("stopped container")

	return nil
}

func (s *workspaceClient) isMachineRunning(ctx context.Context) (bool, error) {
	if !s.isMachineProvider() {
		return true, nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
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
	if s.providerMachine == nil {
		return client.StatusNotFound, nil
	}

	machineClient, err := NewMachineClient(s.devsyConfig, s.providerConfig, s.providerMachine)
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
	workspaceFolder, err := provider.GetWorkspaceDir(
		s.providerWorkspace.Context,
		s.providerWorkspace.ID,
	)
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
		"%s internal agent workspace %s --workspace-info %s",
		shellescape.Quote(info.Agent.Path),
		subcommand,
		shellescape.Quote(compressed),
	), nil
}

func (s *workspaceClient) runProviderCommand(
	ctx context.Context,
	command string,
	stdout, stderr io.Writer,
) error {
	return RunCommandWithBinaries(ctx, WorkspaceCommandConfig{
		Command:              s.providerConfig.Exec.Command,
		WorkspaceContextName: s.providerWorkspace.Context,
		Workspace:            s.providerWorkspace,
		Machine:              s.providerMachine,
		Options:              s.devsyConfig.ProviderOptions(s.providerConfig.Name),
		Config:               s.providerConfig,
		ExtraEnv:             map[string]string{provider.CommandEnv: command},
		Stdout:               stdout,
		Stderr:               stderr,
	})
}

func (s *workspaceClient) buildEnvironment(command string) ([]string, error) {
	s.m.Lock()
	defer s.m.Unlock()

	return provider.ToEnvironmentWithBinaries(provider.EnvironmentOptions{
		Context:   s.providerWorkspace.Context,
		Workspace: s.providerWorkspace,
		Machine:   s.providerMachine,
		Options:   s.devsyConfig.ProviderOptions(s.providerConfig.Name),
		Config:    s.providerConfig,
		ExtraEnv:  map[string]string{provider.CommandEnv: command},
	})
}

func (s *workspaceClient) lockIDs() (string, string, string) {
	s.m.Lock()
	defer s.m.Unlock()

	machineID := ""
	if s.providerMachine != nil {
		machineID = s.providerMachine.ID
	}

	return s.providerWorkspace.Context, s.providerWorkspace.ID, machineID
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

type WorkspaceCommandConfig struct {
	WorkspaceContextName string
	ExtraEnv             map[string]string
	Options              map[string]config.OptionValue
	Stdin                io.Reader
	Stdout               io.Writer
	Stderr               io.Writer
	RawStdout            bool
	Command              types.StrArray
	Workspace            *provider.Workspace
	Machine              *provider.Machine
	Config               *provider.ProviderConfig
	closeInputOnExit     io.Closer
}

func RunCommandWithBinaries(ctx context.Context, cfg WorkspaceCommandConfig) error {
	environ, err := provider.ToEnvironmentWithBinaries(provider.EnvironmentOptions{
		Context:   cfg.WorkspaceContextName,
		Workspace: cfg.Workspace,
		Machine:   cfg.Machine,
		Options:   cfg.Options,
		Config:    cfg.Config,
		ExtraEnv:  cfg.ExtraEnv,
	})
	if err != nil {
		return err
	}

	return RunCommand(ctx, RunCommandOptions{
		Command:          cfg.Command,
		Environ:          environ,
		Stdin:            cfg.Stdin,
		Stdout:           cfg.Stdout,
		Stderr:           cfg.Stderr,
		RawStdout:        cfg.RawStdout,
		Cancellation:     cfg.Config.Exec.Cancellation,
		closeInputOnExit: cfg.closeInputOnExit,
	})
}

type RunCommandOptions struct {
	Command          types.StrArray
	Environ          []string
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	RawStdout        bool
	Cancellation     provider.ProviderCancellationMode
	providerShell    *providerShellInvocation
	closeInputOnExit io.Closer
}

func RunCommand(ctx context.Context, opts RunCommandOptions) error {
	if opts.Cancellation == provider.ProviderCancellationFile {
		return runProviderWithCancellation(ctx, opts)
	}
	return runCommandExecution(ctx, opts)
}

func runCommandExecution(ctx context.Context, opts RunCommandOptions) (retErr error) {
	if len(opts.Command) == 0 {
		return nil
	}

	if log.DebugEnabled() {
		opts.Environ = append(opts.Environ, config.EnvDebug+"="+config.BoolTrue)
	}

	redactor := secrets.NewEnvironmentRedactor(opts.Environ)
	stdout := opts.Stdout
	var redactingStdout *subprocess.StreamingRedactingWriter
	if !opts.RawStdout {
		redactingStdout = &subprocess.StreamingRedactingWriter{
			Next:     opts.Stdout,
			Redactor: redactor,
		}
		stdout = redactingStdout
	}
	stderr := &subprocess.StreamingRedactingWriter{Next: opts.Stderr, Redactor: redactor}
	defer func() {
		flushErr := flushCommandOutput(redactingStdout, stderr)
		if retErr == nil {
			retErr = flushErr
		}
	}()

	if len(opts.Command) == 1 {
		return runProviderShellCommand(ctx, opts, stdout, stderr)
	}
	return runProviderArgvCommand(ctx, opts, stdout, stderr)
}

func runProviderShellCommand(
	ctx context.Context,
	opts RunCommandOptions,
	stdout, stderr io.Writer,
) error {
	if opts.providerShell != nil {
		return runSupervisedProviderShell(ctx, opts, stdout, stderr)
	}
	runner := &shell.CommandRunner{
		Command: opts.Command[0],
		Stdin:   opts.Stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		Environ: opts.Environ,
	}
	if err := shell.RunEmulatedShell(ctx, runner); err != nil {
		return fmt.Errorf("run provider %s: %w", providerAction(opts.Environ), err)
	}
	return nil
}

func runSupervisedProviderShell(
	ctx context.Context,
	opts RunCommandOptions,
	stdout, stderr io.Writer,
) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find provider shell executable: %w", err)
	}
	files := opts.providerShell
	cmd := &exec.Cmd{
		Path: binary,
		Args: []string{
			binary,
			"internal",
			"provider-shell",
			"--script-file",
			files.script,
			"--force-file",
			files.force,
			"--ack-file",
			files.ack,
			"--parent-pid",
			strconv.Itoa(os.Getpid()),
		},
		Env:       opts.Environ,
		Stdin:     opts.Stdin,
		Stdout:    stdout,
		Stderr:    stderr,
		WaitDelay: 2 * time.Second,
	}
	if opts.closeInputOnExit != nil {
		return waitSupervisedProviderShellWithOwnedInput(
			ctx,
			cmd,
			opts.closeInputOnExit,
			providerAction(opts.Environ),
		)
	}
	return waitSupervisedProviderShell(ctx, cmd, providerAction(opts.Environ))
}

type providerShellPipes struct {
	stdinRead   *os.File
	stdinWrite  *os.File
	stdoutRead  *os.File
	stdoutWrite *os.File
	stderrRead  *os.File
	stderrWrite *os.File
}

func openProviderShellPipes() (pipes *providerShellPipes, retErr error) {
	pipes = &providerShellPipes{}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, pipes.close())
		}
	}()
	pipes.stdinRead, pipes.stdinWrite, retErr = os.Pipe()
	if retErr != nil {
		return
	}
	pipes.stdoutRead, pipes.stdoutWrite, retErr = os.Pipe()
	if retErr != nil {
		return
	}
	pipes.stderrRead, pipes.stderrWrite, retErr = os.Pipe()
	if retErr != nil {
		return
	}
	return
}

func closeProviderShellPipe(pipe *os.File) error {
	if pipe == nil {
		return nil
	}
	err := pipe.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func (p *providerShellPipes) close() error {
	return errors.Join(
		closeProviderShellPipe(p.stdinRead),
		closeProviderShellPipe(p.stdinWrite),
		closeProviderShellPipe(p.stdoutRead),
		closeProviderShellPipe(p.stdoutWrite),
		closeProviderShellPipe(p.stderrRead),
		closeProviderShellPipe(p.stderrWrite),
	)
}

func (p *providerShellPipes) closeChildEnds() error {
	err := errors.Join(
		closeProviderShellPipe(p.stdinRead),
		closeProviderShellPipe(p.stdoutWrite),
		closeProviderShellPipe(p.stderrWrite),
	)
	p.stdinRead, p.stdoutWrite, p.stderrWrite = nil, nil, nil
	return err
}

func monitorSupervisedProviderShell(
	ctx context.Context,
	cmd *exec.Cmd,
	stdoutRead, stderrRead *os.File,
) (chan struct{}, <-chan struct{}) {
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			_ = closeProviderShellPipe(stdoutRead)
			_ = closeProviderShellPipe(stderrRead)
		case <-stopped:
		}
	}()
	return stopped, done
}

type providerShellInputResult struct {
	copyErr  error
	closeErr error
}

func pumpProviderShellInput(writer *os.File, source io.Reader) <-chan providerShellInputResult {
	done := make(chan providerShellInputResult, 1)
	go func() {
		if source == nil {
			source = strings.NewReader("")
		}
		_, copyErr := io.Copy(writer, source)
		done <- providerShellInputResult{copyErr, closeProviderShellPipe(writer)}
	}()
	return done
}

type providerShellOutputPump struct {
	reader  *os.File
	done    chan error
	changed chan struct{}
	reading atomic.Bool
}

func (p *providerShellOutputPump) Read(buf []byte) (int, error) {
	p.reading.Store(true)
	p.signal()
	n, err := p.reader.Read(buf)
	p.reading.Store(false)
	p.signal()
	return n, err
}

func (p *providerShellOutputPump) signal() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

func pumpProviderShellOutput(reader *os.File, writer io.Writer) *providerShellOutputPump {
	pump := &providerShellOutputPump{
		reader: reader, done: make(chan error, 1), changed: make(chan struct{}, 1),
	}
	go func() {
		if writer == nil {
			writer = io.Discard
		}
		_, copyErr := io.Copy(writer, pump)
		if copyErr != nil {
			_, _ = io.Copy(io.Discard, pump)
		}
		pump.done <- copyErr
	}()
	return pump
}

func providerShellInputCopyError(copyErr, waitErr, closeErr error) error {
	if waitErr == nil && closeErr == nil &&
		(errors.Is(copyErr, io.ErrClosedPipe) || errors.Is(copyErr, net.ErrClosed) ||
			errors.Is(copyErr, os.ErrClosed) || errors.Is(copyErr, syscall.EPIPE)) {
		return nil
	}
	return copyErr
}

func providerShellCopyError(action, stream string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("copy provider %s %s: %w", action, stream, err)
}

func providerShellCloseError(action, target string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close provider %s %s: %w", action, target, err)
}

func providerShellWaitError(action string, err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code := exitErr.ExitCode()
		if code >= 0 && code <= 255 {
			err = interp.ExitStatus(uint8(code))
		}
	}
	return fmt.Errorf("run provider %s: %w", action, err)
}

func waitSupervisedProviderShellWithOwnedInput(
	ctx context.Context,
	cmd *exec.Cmd,
	input io.Closer,
	action string,
) (retErr error) {
	pipes, err := openProviderShellPipes()
	if err != nil {
		return fmt.Errorf("create provider %s stdio pipes: %w", action, err)
	}
	defer func() { retErr = errors.Join(retErr, pipes.close()) }()

	source, stdout, stderr := cmd.Stdin, cmd.Stdout, cmd.Stderr
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pipes.stdinRead, pipes.stdoutWrite, pipes.stderrWrite
	configureProviderShellProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start provider %s shell: %w", action, err)
	}
	pipeCloseErr := pipes.closeChildEnds()
	stopped, monitorDone := monitorSupervisedProviderShell(
		ctx, cmd, pipes.stdoutRead, pipes.stderrRead,
	)
	stdinDone := pumpProviderShellInput(pipes.stdinWrite, source)
	stdoutPump := pumpProviderShellOutput(pipes.stdoutRead, stdout)
	stderrPump := pumpProviderShellOutput(pipes.stderrRead, stderr)
	waitErr := cmd.Wait()
	stdoutErr, stderrErr, drainErr := drainProviderShellOutput(
		pipes, stdoutPump, stderrPump, action,
	)
	close(stopped)
	<-monitorDone
	closeErr := input.Close()
	stdinCloseErr := closeProviderShellPipe(pipes.stdinWrite)
	stdinResult := <-stdinDone
	stdinErr := providerShellInputCopyError(stdinResult.copyErr, waitErr, closeErr)
	return errors.Join(
		drainErr,
		providerShellWaitError(action, waitErr),
		providerShellCopyError(action, "stdout", stdoutErr),
		providerShellCopyError(action, "stderr", stderrErr),
		providerShellCopyError(action, "stdin", stdinErr),
		providerShellCloseError(
			action,
			"stdin pipe",
			errors.Join(stdinCloseErr, stdinResult.closeErr),
		),
		providerShellCloseError(action, "stdio pipes", pipeCloseErr),
		providerShellCloseError(action, "input", closeErr),
	)
}

type providerShellOutputDrain struct {
	pipes      *providerShellPipes
	stdoutPump *providerShellOutputPump
	stderrPump *providerShellOutputPump
	stdoutDone <-chan error
	stderrDone <-chan error
	timer      *time.Timer
	timeout    <-chan time.Time
	action     string
	stdoutErr  error
	stderrErr  error
	drainErr   error
}

func drainProviderShellOutput(
	pipes *providerShellPipes,
	stdoutPump, stderrPump *providerShellOutputPump,
	action string,
) (stdoutErr, stderrErr, drainErr error) {
	timer := time.NewTimer(2 * time.Second)
	timer.Stop()
	defer timer.Stop()
	drain := providerShellOutputDrain{
		pipes: pipes, stdoutPump: stdoutPump, stderrPump: stderrPump,
		stdoutDone: stdoutPump.done, stderrDone: stderrPump.done,
		timer: timer, action: action,
	}
	for drain.stdoutDone != nil || drain.stderrDone != nil {
		drain.updateTimeout()
		select {
		case drain.stdoutErr = <-drain.stdoutDone:
			drain.stdoutDone = nil
		case drain.stderrErr = <-drain.stderrDone:
			drain.stderrDone = nil
		case <-stdoutPump.changed:
			drain.resetTimeout()
		case <-stderrPump.changed:
			drain.resetTimeout()
		case <-drain.timeout:
			drain.handleTimeout()
		}
	}
	return drain.stdoutErr, drain.stderrErr, drain.drainErr
}

func (d *providerShellOutputDrain) idle() bool {
	return providerShellOutputIdle(d.stdoutPump, d.stdoutDone) &&
		providerShellOutputIdle(d.stderrPump, d.stderrDone)
}

func (d *providerShellOutputDrain) updateTimeout() {
	if d.idle() {
		if d.timeout == nil {
			d.timer.Reset(2 * time.Second)
			d.timeout = d.timer.C
		}
	} else if d.timeout != nil {
		d.timer.Stop()
		d.timeout = nil
	}
}

func (d *providerShellOutputDrain) resetTimeout() {
	if d.timeout != nil {
		d.timer.Reset(2 * time.Second)
	}
}

func (d *providerShellOutputDrain) handleTimeout() {
	d.timeout = nil
	if !d.idle() || d.recentOutput() {
		return
	}
	d.stdoutDone, d.stdoutErr = readyProviderShellOutput(d.stdoutDone, d.stdoutErr)
	d.stderrDone, d.stderrErr = readyProviderShellOutput(d.stderrDone, d.stderrErr)
	d.drainErr = closeUndrainedProviderShellOutput(
		d.pipes, d.stdoutDone, d.stderrDone, d.action,
	)
}

func (d *providerShellOutputDrain) recentOutput() bool {
	select {
	case <-d.stdoutPump.changed:
		return true
	default:
	}
	select {
	case <-d.stderrPump.changed:
		return true
	default:
	}
	return false
}

func providerShellOutputIdle(pump *providerShellOutputPump, done <-chan error) bool {
	return done == nil || pump.reading.Load()
}

func readyProviderShellOutput(done <-chan error, current error) (<-chan error, error) {
	if done == nil {
		return nil, current
	}
	select {
	case err := <-done:
		return nil, err
	default:
		return done, current
	}
}

func closeUndrainedProviderShellOutput(
	pipes *providerShellPipes,
	stdoutDone, stderrDone <-chan error,
	action string,
) error {
	if stdoutDone == nil && stderrDone == nil {
		return nil
	}
	if stdoutDone != nil {
		_ = closeProviderShellPipe(pipes.stdoutRead)
	}
	if stderrDone != nil {
		_ = closeProviderShellPipe(pipes.stderrRead)
	}
	return fmt.Errorf("drain provider %s output after shell exit: %w", action, exec.ErrWaitDelay)
}

func waitSupervisedProviderShell(ctx context.Context, cmd *exec.Cmd, action string) error {
	configureProviderShellProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start provider %s shell: %w", action, err)
	}
	stopped, monitorDone := monitorSupervisedProviderShell(ctx, cmd, nil, nil)
	err := cmd.Wait()
	close(stopped)
	<-monitorDone
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code := exitErr.ExitCode()
			if code >= 0 && code <= 255 {
				err = interp.ExitStatus(uint8(code))
			}
		}
		return fmt.Errorf("run provider %s: %w", action, err)
	}
	return nil
}

func runProviderArgvCommand(
	ctx context.Context,
	opts RunCommandOptions,
	stdout, stderr io.Writer,
) error {
	cmd := exec.CommandContext(ctx, opts.Command[0], opts.Command[1:]...) // #nosec G204
	cmd.Stdin = opts.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = opts.Environ
	if opts.Cancellation == provider.ProviderCancellationFile {
		cmd.WaitDelay = 2 * time.Second
		if opts.closeInputOnExit != nil {
			return runProviderArgvWithOwnedInput(
				cmd, opts.Stdin, opts.closeInputOnExit, providerAction(opts.Environ),
			)
		}
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run provider %s: %w", providerAction(opts.Environ), err)
	}
	return nil
}

func runProviderArgvWithOwnedInput(
	cmd *exec.Cmd,
	source io.Reader,
	input io.Closer,
	action string,
) (retErr error) {
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return errors.Join(
			fmt.Errorf("create provider %s stdin pipe: %w", action, err),
			providerShellCloseError(action, "input", input.Close()),
		)
	}
	defer func() {
		retErr = errors.Join(
			retErr, closeProviderShellPipe(stdinRead), closeProviderShellPipe(stdinWrite),
		)
	}()
	cmd.Stdin = stdinRead
	if err := cmd.Start(); err != nil {
		return errors.Join(
			fmt.Errorf("run provider %s: %w", action, err),
			providerShellCloseError(action, "input", input.Close()),
		)
	}
	childPipeCloseErr := closeProviderShellPipe(stdinRead)
	stdinDone := pumpProviderShellInput(stdinWrite, source)
	waitErr := cmd.Wait()
	inputCloseErr := input.Close()
	stdinPipeCloseErr := closeProviderShellPipe(stdinWrite)
	stdinResult := <-stdinDone
	stdinErr := providerShellInputCopyError(stdinResult.copyErr, waitErr, inputCloseErr)
	return errors.Join(
		providerArgvWaitError(action, waitErr),
		providerShellCopyError(action, "stdin", stdinErr),
		providerShellCloseError(
			action, "stdin pipe",
			errors.Join(childPipeCloseErr, stdinPipeCloseErr, stdinResult.closeErr),
		),
		providerShellCloseError(action, "input", inputCloseErr),
	)
}

func providerArgvWaitError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("run provider %s: %w", action, err)
}

func flushCommandOutput(stdout, stderr *subprocess.StreamingRedactingWriter) error {
	var stdoutErr error
	if stdout != nil {
		if err := stdout.Flush(); err != nil {
			stdoutErr = fmt.Errorf("flush provider stdout: %w", err)
		}
	}
	var stderrErr error
	if err := stderr.Flush(); err != nil {
		stderrErr = fmt.Errorf("flush provider stderr: %w", err)
	}
	return errors.Join(stdoutErr, stderrErr)
}

const providerCommandAction = "command"

func providerAction(environ []string) string {
	for _, kv := range environ {
		if strings.HasPrefix(kv, provider.CommandEnv+"=") {
			return providerCommandAction
		}
	}
	return "lifecycle"
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
	// PreserveContent leaves the content directory intact when only managed records are owned.
	PreserveContent bool
}

func DeleteWorkspaceFolder(params DeleteWorkspaceFolderParams) error {
	if err := removeSSHConfig(params); err != nil {
		return err
	}

	if err := removeWorkspaceDir(params.Context, params.WorkspaceID); err != nil {
		return err
	}

	if params.PreserveContent {
		return nil
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

	owner, err := removeSSHOwner(params)
	if err != nil {
		log.Errorf("resolve ssh owner for workspace %q: %v", params.WorkspaceID, err)
		return nil
	}

	if err := ssh.RemoveFromConfigForOwner(
		owner,
		params.WorkspaceID,
		sshConfigPath,
		sshConfigIncludePath,
	); err != nil {
		log.Errorf("remove workspace %q from ssh config: %v", params.WorkspaceID, err)
	}

	return nil
}

func removeSSHOwner(params DeleteWorkspaceFolderParams) (ssh.Owner, error) {
	uid := ""
	if ws, err := provider.LoadWorkspaceConfig(params.Context, params.WorkspaceID); err == nil {
		uid = ws.UID
	}

	return ssh.NewOwner(params.Context, uid)
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
	errChan := runAgentInjection(cancelCtx, workspaceInjectionConfig{
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
		"%s internal agent workspace %s --workspace-info %s",
		shellescape.Quote(workspaceClient.AgentPath()),
		agentCommand,
		shellescape.Quote(workspaceInfo),
	)
	if log.DebugEnabled() {
		command += " --debug"
	}
	return command
}

type processIOPipes struct {
	stdoutReader *os.File
	stdoutWriter *os.File
	stdinReader  *os.File
	stdinWriter  *os.File
}

func newAgentPipes() (*processIOPipes, error) {
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

	return &processIOPipes{
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
		stdinReader:  stdinReader,
		stdinWriter:  stdinWriter,
	}, nil
}

func (p *processIOPipes) close() {
	_ = p.stdoutWriter.Close()
	_ = p.stdoutReader.Close()
	_ = p.stdinReader.Close()
	_ = p.stdinWriter.Close()
}

type workspaceInjectionConfig struct {
	workspaceClient client.WorkspaceClient
	command         string
	stdin           *os.File
	stdout          *os.File
	timeout         time.Duration
	cancel          context.CancelFunc
}

func runAgentInjection(ctx context.Context, cfg workspaceInjectionConfig) chan error {
	errChan := make(chan error, 1)
	go func() {
		streamer := log.NewJSONLogStreamer(log.StreamerOptions{
			FallbackLevel:       log.LevelInfo,
			DetectLevelPrefixes: true,
		})
		defer func() {
			log.Debugf("up command completed")
			cfg.cancel()
			_ = streamer.Close()
		}()

		errChan <- agent.InjectAgent(ctx, &agent.InjectOptions{
			Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
				return cfg.workspaceClient.Command(ctx, client.CommandOptions{
					Command:   command,
					Stdin:     stdin,
					Stdout:    stdout,
					Stderr:    stderr,
					RawStdout: true,
				})
			},
			IsLocal:         cfg.workspaceClient.AgentLocal(),
			RemoteAgentPath: cfg.workspaceClient.AgentPath(),
			DownloadURL:     cfg.workspaceClient.AgentURL(),
			Command:         cfg.command,
			Stdin:           cfg.stdin,
			Stdout:          cfg.stdout,
			Stderr:          streamer,
			Timeout:         cfg.timeout,
		})
	}()
	return errChan
}

func runTunnelServer(
	ctx context.Context,
	opts BuildAgentClientOptions,
	stdoutReader, stdinWriter *os.File,
) (*config2.Result, error) {
	tunnelOptions := []tunnelserver.Option{
		tunnelserver.WithStatusReporter(status.NewLogReporter()),
		tunnelserver.WithGitToken(opts.CLIOptions.GitToken),
	}
	tunnelOptions = append(tunnelOptions, opts.TunnelOptions...)

	result, err := tunnelserver.RunUpServer(ctx, tunnelserver.UpSpec{
		Reader:                 stdoutReader,
		Writer:                 stdinWriter,
		AllowGitCredentials:    opts.WorkspaceClient.AgentInjectGitCredentials(opts.CLIOptions),
		AllowDockerCredentials: opts.WorkspaceClient.AgentInjectDockerCredentials(opts.CLIOptions),
		Workspace:              opts.WorkspaceClient.WorkspaceConfig(),
		Options:                tunnelOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("run tunnel server: %w", err)
	}
	return result, nil
}
