package clientimplementation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/options"
	"github.com/devsy-org/devsy/pkg/options/resolver"
	platformclient "github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/terminal"
	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/devsy-org/devsy/pkg/types"
)

func NewProxyClient(
	devsyConfig *config.Config,
	prov *provider.ProviderConfig,
	workspace *provider.Workspace,
) (client.ProxyClient, error) {
	pc := &proxyClient{
		devsyConfig: devsyConfig,
		config:      prov,
		workspace:   workspace,
	}
	pc.executor = &proxyExecutor{client: pc}
	pc.locks = newClientLocks(pc.lockIDs)
	return pc, nil
}

type proxyClient struct {
	m sync.Mutex

	locks *clientLocks

	devsyConfig *config.Config
	config      *provider.ProviderConfig
	workspace   *provider.Workspace
	executor    *proxyExecutor
}

// proxyExecutor handles proxy command execution with common patterns.
type proxyExecutor struct {
	client *proxyClient
}

type execParams struct {
	operation        string
	command          types.StrArray
	extraEnv         map[string]string
	stdin            io.Reader
	closeInputOnExit io.Closer
	stdout           io.Writer
	stderr           io.Writer
	rawStdout        bool
}

// execute runs a proxy command with common settings.
func (e *proxyExecutor) execute(ctx context.Context, params execParams) error {
	var started time.Time
	if telemetry.MetricsFromContext(ctx) != nil {
		started = time.Now()
	}
	err := RunCommandWithBinaries(ctx, WorkspaceCommandConfig{
		Command:              params.command,
		WorkspaceContextName: e.client.workspace.Context,
		Workspace:            e.client.workspace,
		Options:              e.client.devsyConfig.ProviderOptions(e.client.config.Name),
		Config:               e.client.config,
		ExtraEnv:             params.extraEnv,
		Stdin:                params.stdin,
		closeInputOnExit:     params.closeInputOnExit,
		Stdout:               params.stdout,
		Stderr:               params.stderr,
		RawStdout:            params.rawStdout,
	})
	if !started.IsZero() {
		code := ""
		if err != nil {
			code = string(clierr.Classify(err).Code)
		}
		telemetry.RecordProviderOperation(ctx, telemetry.ProviderOperation{
			ProviderType: telemetry.ProviderTypeScriptProxy,
			Operation:    params.operation,
			Duration:     time.Since(started),
			ErrorCode:    code,
		})
	}
	return err
}

func (e *proxyExecutor) executeWithJSONLog(ctx context.Context, params execParams) error {
	writer := log.NewJSONLogStreamer(log.StreamerOptions{
		FallbackLevel:       log.SlogLevelWarn,
		DetectLevelPrefixes: true,
	})
	if params.stderr == nil {
		params.stderr = writer
	} else {
		params.stderr = io.MultiWriter(writer, params.stderr)
	}
	err := e.execute(ctx, params)
	_ = writer.Close()
	return err
}

func (s *proxyClient) Lock(ctx context.Context) error {
	if err := s.locks.ensureOpen(); err != nil {
		return fmt.Errorf("init lock: %w", err)
	}

	// try to lock workspace
	log.Debugf("Acquire workspace lock")
	if err := s.locks.lockWorkspace(ctx); err != nil {
		return fmt.Errorf("error locking workspace: %w", err)
	}
	log.Debugf("Acquired workspace lock")

	return nil
}

func (s *proxyClient) Unlock() {
	if err := s.locks.ensureOpen(); err != nil {
		log.Warnf("init lock: %v", err)
		return
	}

	// try to unlock workspace
	if err := s.locks.unlockWorkspace(); err != nil {
		log.Warnf("Error unlocking workspace: %v", err)
	}
}

func (s *proxyClient) Provider() string {
	return s.config.Name
}

func (s *proxyClient) Workspace() string {
	s.m.Lock()
	defer s.m.Unlock()

	return s.workspace.ID
}

func (s *proxyClient) WorkspaceConfig() *provider.Workspace {
	s.m.Lock()
	defer s.m.Unlock()

	return provider.CloneWorkspace(s.workspace)
}

func (s *proxyClient) Context() string {
	return s.workspace.Context
}

func (s *proxyClient) RefreshOptions(
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

	workspace, err := options.ResolveAndSaveOptionsWorkspace(ctx, options.ResolveWorkspaceRequest{
		DevConfig:         s.devsyConfig,
		ProviderConfig:    s.config,
		OriginalWorkspace: s.workspace,
		UserOptions:       userOptions,
		Options:           []resolver.Option{resolver.WithResolveSubOptions()},
	})
	if err != nil {
		return err
	}

	if reconfigure {
		err := s.updateInstance(ctx)
		if err != nil {
			return err
		}
	}

	s.workspace = workspace
	return nil
}

func (s *proxyClient) Create(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	err := s.executor.execute(ctx, execParams{
		operation: "create",
		command:   s.config.Exec.Proxy.Create.Workspace,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
	})
	if err != nil {
		return fmt.Errorf("create remote workspace : %w", err)
	}
	return nil
}

func (s *proxyClient) Ssh(ctx context.Context, opt client.SshOptions) error {
	return s.executor.executeWithJSONLog(ctx, execParams{
		operation: "command",
		command:   s.config.Exec.Proxy.Ssh,
		extraEnv:  EncodeOptions(opt, config.EnvFlagsSSH),
		stdin:     opt.Stdin,
		stdout:    opt.Stdout,
		rawStdout: true,
	})
}

func (s *proxyClient) OpenSSHTransport(
	ctx context.Context,
	opt client.SSHTransportOptions,
) (transport.ManagedConn, error) {
	return transport.OpenCallbackConn(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return s.executor.executeWithJSONLog(ctx, execParams{
				operation: "command",
				command:   s.config.Exec.Proxy.Ssh,
				extraEnv: EncodeOptions(
					client.SshOptions{User: opt.User},
					config.EnvFlagsSSH,
				),
				stdin:            stdin,
				closeInputOnExit: stdin.(io.Closer),
				stdout:           stdout,
				rawStdout:        true,
			})
		},
		transport.CallbackConnOptions{
			LocalAddr:  transport.NewAddr("proxy"),
			RemoteAddr: transport.NewAddr("provider:" + s.config.Name),
		},
	)
}

func (s *proxyClient) Stop(ctx context.Context, opt client.StopOptions) error {
	s.m.Lock()
	defer s.m.Unlock()

	return s.executor.executeWithJSONLog(ctx, execParams{
		operation: "stop",
		command:   s.config.Exec.Proxy.Stop,
		stdout:    io.Discard,
	})
}

func (s *proxyClient) Up(ctx context.Context, opt client.UpOptions) error {
	level, err := opt.EffectiveLogLevel()
	if err != nil {
		return err
	}
	opts := EncodeOptions(opt.CLIOptions, config.EnvFlagsUp)
	opts[config.EnvLogLevel] = level
	opts["DEVSY_LOG_FORMAT"] = "json"

	providerOptions := s.devsyConfig.ProviderOptions(s.config.Name)
	if err := s.checkPlatformVersion(ctx, providerOptions); err != nil {
		return err
	}

	return s.executor.executeWithJSONLog(ctx, execParams{
		operation: "start",
		command:   s.config.Exec.Proxy.Up,
		extraEnv:  opts,
		stdin:     opt.Stdin,
		stdout:    opt.Stdout,
		rawStdout: true,
	})
}

func (s *proxyClient) Delete(ctx context.Context, opt client.DeleteOptions) error {
	s.m.Lock()
	defer s.m.Unlock()

	if opt.GracePeriod != "" {
		if duration, err := time.ParseDuration(opt.GracePeriod); err == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, duration)
			defer cancel()
		}
	}

	err := s.executor.executeWithJSONLog(ctx, execParams{
		operation: "delete",
		command:   s.config.Exec.Proxy.Delete,
		extraEnv:  EncodeOptions(opt, config.EnvFlagsDelete),
		stdout:    io.Discard,
	})
	if err != nil && !opt.Force {
		return fmt.Errorf("error deleting workspace: %w", err)
	}
	if err != nil {
		log.Errorf("Error deleting workspace: %v", err)
	}

	return DeleteWorkspaceFolder(DeleteWorkspaceFolderParams{
		Context:              s.workspace.Context,
		WorkspaceID:          s.workspace.ID,
		SSHConfigPath:        s.workspace.SSHConfigPath,
		SSHConfigIncludePath: s.workspace.SSHConfigIncludePath,
	})
}

func (s *proxyClient) Status(
	ctx context.Context,
	options client.StatusOptions,
) (client.Status, error) {
	s.m.Lock()
	defer s.m.Unlock()

	stdout := &bytes.Buffer{}
	providerOptions := s.devsyConfig.ProviderOptions(s.config.Name)
	resolvedOptions := provider.CombineOptions(s.workspace, nil, providerOptions)
	extraEnv := EncodeOptions(options, config.EnvFlagsStatus)
	environment := provider.ToEnvironment(s.workspace, nil, providerOptions, extraEnv)
	var protected []string
	for _, definitions := range []config.OptionDefinitions{
		s.config.Options,
		s.devsyConfig.DynamicProviderOptionDefinitions(s.config.Name),
	} {
		for name, definition := range definitions {
			if definition != nil && (definition.Password || definition.Hidden) {
				if value, ok := resolvedOptions[name]; ok {
					protected = append(protected, name+"="+value.Value)
				}
			}
		}
	}
	stderr := log.NewJSONLogStreamer(log.StreamerOptions{
		Redactor: secrets.Combine(
			secrets.NewEnvironmentRedactor(environment),
			secrets.NewRedactor(protected),
		),
		FallbackLevel:       log.SlogLevelError,
		DetectLevelPrefixes: true,
		CaptureLines:        50,
		CaptureBytes:        32 * 1024,
		DefaultAttrs:        []slog.Attr{log.AttrProvider(s.config.Name)},
	})
	defer func() { _ = stderr.Close() }()
	err := s.executor.execute(ctx, execParams{
		operation: "status",
		command:   s.config.Exec.Proxy.Status,
		extraEnv:  extraEnv,
		stdout:    stdout,
		stderr:    stderr,
		rawStdout: true,
	})
	_ = stderr.Close()
	if err != nil {
		cause := fmt.Errorf("error retrieving container status: %w", err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return client.StatusNotFound, cause
		}
		message := "error retrieving container status"
		if tail := stderr.ErrorOutput(); tail != "" {
			message += ": " + tail
		}
		code := clierr.CodeUnknown
		var classified *clierr.CLIError
		if errors.As(err, &classified) && classified != nil {
			code = classified.Code
		}
		return client.StatusNotFound, clierr.ProviderFailure(cause, message, code)
	}

	status := &client.WorkspaceStatus{}
	if err := json.Unmarshal(stdout.Bytes(), status); err != nil {
		cause := fmt.Errorf("error parsing proxy command response: %w", err)
		if tail := stderr.ErrorOutput(); tail != "" {
			return client.StatusNotFound, clierr.WithContext(
				cause,
				map[string]string{"stderr": tail},
			)
		}
		return client.StatusNotFound, cause
	}
	parsed, err := client.ParseStatus(status.State)
	if err != nil {
		return client.StatusNotFound, clierr.ProviderFailure(
			err,
			"unrecognized proxy status",
			clierr.CodeUnknown,
		)
	}
	return parsed, nil
}

func (s *proxyClient) lockIDs() (string, string, string) {
	s.m.Lock()
	defer s.m.Unlock()

	return s.workspace.Context, s.workspace.ID, ""
}

// checkPlatformVersion validates the platform provider version compatibility.
func (s *proxyClient) checkPlatformVersion(
	ctx context.Context,
	providerOptions map[string]config.OptionValue,
) error {
	devsyConfigPath := providerOptions["DEVSY_CONFIG"].Value
	if devsyConfigPath == "" {
		return nil
	}

	baseClient, err := platformclient.InitClientFromPath(ctx, devsyConfigPath)
	if err != nil {
		return fmt.Errorf("error initializing platform client: %w", err)
	}

	version, err := baseClient.Version()
	if err != nil {
		return fmt.Errorf("error retrieving platform version: %w", err)
	}

	parsedVersion, err := semver.Parse(strings.TrimPrefix(version.DevsyVersion, "v"))
	if err != nil {
		return fmt.Errorf("error parsing platform version: %w", err)
	}

	if parsedVersion.GE(semver.MustParse("0.6.99")) {
		return fmt.Errorf(
			"you are using an outdated provider version for this platform. " +
				"Disconnect and reconnect the platform to update the provider",
		)
	}

	return nil
}

func (s *proxyClient) updateInstance(ctx context.Context) error {
	if !terminal.IsTerminalIn {
		return fmt.Errorf("unable to update instance through CLI if stdin is not a terminal")
	}

	return s.executor.execute(ctx, execParams{
		operation: "other",
		command:   s.config.Exec.Proxy.Update.Workspace,
		stdin:     os.Stdin,
		stdout:    os.Stdout,
		stderr:    os.Stderr,
	})
}

func EncodeOptions(options any, name string) map[string]string {
	raw, _ := json.Marshal(options)
	return map[string]string{
		name: string(raw),
	}
}

func DecodeOptionsFromEnv(name string, into any) (bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return false, nil
	}

	return true, json.Unmarshal([]byte(raw), into)
}

func DecodePlatformOptionsFromEnv(into *devsy.PlatformOptions) error {
	raw := os.Getenv(config.EnvPlatformOptions)
	if raw == "" {
		return nil
	}

	return json.Unmarshal([]byte(raw), into)
}
