package clientimplementation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/options"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/devsy-org/devsy/pkg/types"
)

func NewMachineClient(
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
	machine *provider.Machine,
) (client.MachineClient, error) {
	if !provider.IsMachineProvider() {
		log.Error("provider is not a machine provider")
		return nil, fmt.Errorf("Provider is not a machine provider. Use another provider")
	} else if machine == nil {
		return nil, fmt.Errorf(
			"Machine does not exist. Perhaps it was deleted without the workspace being deleted",
		)
	}

	mc := &machineClient{
		devsyConfig: devsyConfig,
		config:      provider,
		machine:     machine,
	}
	mc.executor = &machineExecutor{client: mc}

	return mc, nil
}

type machineClient struct {
	devsyConfig *config.Config
	config      *provider.ProviderConfig
	machine     *provider.Machine
	executor    *machineExecutor
}

// machineExecutor handles command execution with common patterns.
type machineExecutor struct {
	client *machineClient
}

// execConfig defines how to execute a machine command.
type execConfig struct {
	name             string
	command          types.StrArray
	stdout           io.Writer
	stderr           io.Writer
	extraEnv         map[string]string
	stdin            io.Reader
	closeInputOnExit io.Closer
	rawStdout        bool
	diagnostic       bool
	withProgress     bool
	startMsg         string
	doneMsg          string
}

func (e *machineExecutor) execute(ctx context.Context, cfg execConfig) (err error) {
	started := time.Now()
	defer func() {
		code := ""
		if err != nil {
			code = string(clierr.Classify(err).Code)
		}
		telemetry.RecordProviderOperation(ctx, telemetry.ProviderOperation{
			ProviderType: telemetry.ProviderTypeMachineBinary,
			Operation:    cfg.name,
			Duration:     time.Since(started),
			ErrorCode:    code,
		})
	}()
	var streamer *log.JSONLogStreamer
	if cfg.diagnostic {
		streamer = machineDiagnosticStreamer(e.client, cfg.name)
		cfg.stderr = streamer
	}
	var done chan struct{}
	if cfg.withProgress {
		done = scheduleLogMessage("Devsy " + cfg.name + " operation is in progress")
		defer close(done)
	}

	if cfg.startMsg != "" {
		log.InfoContext(ctx, "Machine operation started",
			log.AttrProvider(e.client.config.Name),
			log.AttrMachineID(e.client.machine.ID),
			log.AttrOperation(cfg.name),
			slog.String("description", cfg.startMsg))
	}

	err = RunCommandWithBinaries(ctx, WorkspaceCommandConfig{
		Command:              cfg.command,
		WorkspaceContextName: e.client.machine.Context,
		Machine:              e.client.machine,
		Options:              e.client.devsyConfig.ProviderOptions(e.client.config.Name),
		Config:               e.client.config,
		Stdout:               cfg.stdout,
		Stderr:               cfg.stderr,
		ExtraEnv:             cfg.extraEnv,
		Stdin:                cfg.stdin,
		closeInputOnExit:     cfg.closeInputOnExit,
		RawStdout:            cfg.rawStdout,
	})
	err = finishMachineDiagnostics(streamer, err)
	if err != nil {
		return err
	}

	if cfg.doneMsg != "" {
		log.InfoContext(ctx, "Machine operation completed",
			log.AttrProvider(e.client.config.Name),
			log.AttrMachineID(e.client.machine.ID),
			log.AttrOperation(cfg.name),
			slog.String("description", cfg.doneMsg))
	}

	return nil
}

func machineDiagnosticStreamer(client *machineClient, name string) *log.JSONLogStreamer {
	return log.NewJSONLogStreamer(log.StreamerOptions{
		FallbackLevel: slog.LevelInfo,
		CaptureLines:  50,
		CaptureBytes:  32 * 1024,
		DefaultAttrs: []slog.Attr{
			log.AttrProvider(client.config.Name),
			log.AttrMachineID(client.machine.ID),
			log.AttrOperation(name),
		},
	})
}

func finishMachineDiagnostics(streamer *log.JSONLogStreamer, err error) error {
	if streamer == nil {
		return err
	}
	_ = streamer.Close()
	if err != nil && !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) {
		return clierr.ProviderFailure(err, streamer.ErrorOutput(), clierr.CodeUnknown)
	}
	return err
}

// lifecycleCommand executes a standard lifecycle operation (create/start/stop).
// lifecycleStep is one lifecycle command of a machine and its progress wording.
type lifecycleStep struct {
	name     string
	command  types.StrArray
	verb     string
	pastVerb string
}

func (e *machineExecutor) lifecycleCommand(ctx context.Context, step lifecycleStep) error {
	name, command := step.name, step.command
	verb, pastVerb := step.verb, step.pastVerb
	writer := log.Writer(slog.LevelInfo)
	defer func() { _ = writer.Close() }()

	return e.execute(ctx, execConfig{
		name:         name,
		command:      command,
		stdout:       writer,
		diagnostic:   true,
		withProgress: true,
		startMsg:     verb + " machine",
		doneMsg:      pastVerb + " machine",
	})
}

func (s *machineClient) Provider() string {
	return s.config.Name
}

func (s *machineClient) Machine() string {
	return s.machine.ID
}

func (s *machineClient) MachineConfig() *provider.Machine {
	return provider.CloneMachine(s.machine)
}

func (s *machineClient) RefreshOptions(
	ctx context.Context,
	userOptionsRaw []string,
	reconfigure bool,
) error {
	userOptions, err := provider.ParseOptions(userOptionsRaw)
	if err != nil {
		return fmt.Errorf("parse options: %w", err)
	}

	machine, err := options.ResolveAndSaveOptionsMachine(ctx, options.ResolveMachineRequest{
		DevConfig:       s.devsyConfig,
		ProviderConfig:  s.config,
		OriginalMachine: s.machine,
		UserOptions:     userOptions,
	})
	if err != nil {
		return err
	}

	s.machine = machine
	return nil
}

func (s *machineClient) AgentPath() string {
	return options.ResolveAgentConfig(s.devsyConfig, s.config, nil, s.machine).Path
}

func (s *machineClient) AgentLocal() bool {
	return options.ResolveAgentConfig(
		s.devsyConfig,
		s.config,
		nil,
		s.machine,
	).Local == config.BoolTrue
}

func (s *machineClient) AgentURL() string {
	return options.ResolveAgentConfig(s.devsyConfig, s.config, nil, s.machine).DownloadURL
}

func (s *machineClient) Context() string {
	return s.machine.Context
}

func (s *machineClient) Create(ctx context.Context) error {
	return status.RunStep(
		ctx,
		status.PhaseMachineCreate,
		"create machine",
		func(ctx context.Context) error {
			return s.executor.lifecycleCommand(ctx, lifecycleStep{
				name:     "create",
				command:  s.config.Exec.Create,
				verb:     "creating",
				pastVerb: "created",
			})
		},
	)
}

func (s *machineClient) Start(ctx context.Context) error {
	return status.RunStep(
		ctx,
		status.PhaseMachineStart,
		"start machine",
		func(ctx context.Context) error {
			return s.executor.lifecycleCommand(ctx, lifecycleStep{
				name: "start", command: s.config.Exec.Start, verb: "starting", pastVerb: "started",
			})
		},
	)
}

func (s *machineClient) Stop(ctx context.Context, options client.StopOptions) error {
	return status.RunStep(
		ctx,
		status.PhaseMachineStop,
		"stop machine",
		func(ctx context.Context) error {
			return s.executor.lifecycleCommand(ctx, lifecycleStep{
				name: "stop", command: s.config.Exec.Stop, verb: "stopping", pastVerb: "stopped",
			})
		},
	)
}

func (s *machineClient) Command(ctx context.Context, commandOptions client.CommandOptions) error {
	return s.executor.execute(ctx, execConfig{
		name:      providerCommandAction,
		command:   s.config.Exec.Command,
		stdout:    commandOptions.Stdout,
		stderr:    commandOptions.Stderr,
		stdin:     commandOptions.Stdin,
		rawStdout: commandOptions.RawStdout,
		extraEnv: map[string]string{
			provider.CommandEnv: commandOptions.Command,
		},
	})
}

func (s *machineClient) OpenCommandTransport(
	ctx context.Context,
	opt client.CommandTransportOptions,
) (transport.ManagedConn, error) {
	return transport.OpenCallbackConn(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return s.executor.execute(ctx, execConfig{
				name:             providerCommandAction,
				command:          s.config.Exec.Command,
				stdout:           stdout,
				stderr:           opt.Stderr,
				stdin:            stdin,
				closeInputOnExit: stdin.(io.Closer),
				rawStdout:        true,
				extraEnv: map[string]string{
					provider.CommandEnv: opt.Command,
				},
			})
		},
		transport.CallbackConnOptions{
			LocalAddr:  transport.NewAddr("machine"),
			RemoteAddr: transport.NewAddr("provider:" + s.Provider()),
		},
	)
}

func (s *machineClient) Status(
	ctx context.Context,
	options client.StatusOptions,
) (client.Status, error) {
	result := client.Status(client.StatusNotFound)
	err := status.RunStep(
		ctx,
		status.PhaseMachineStatus,
		"get machine status",
		func(ctx context.Context) error {
			stdout := &bytes.Buffer{}
			if err := s.executor.execute(ctx, execConfig{
				name:       "status",
				command:    s.config.Exec.Status,
				stdout:     stdout,
				diagnostic: true,
			}); err != nil {
				return fmt.Errorf("get status: %w", err)
			}
			var err error
			result, err = client.ParseStatus(stdout.String())
			return err
		},
	)
	return result, err
}

func (s *machineClient) Describe(ctx context.Context) (string, error) {
	stdout := &bytes.Buffer{}

	err := s.executor.execute(ctx, execConfig{
		name:       "describe",
		command:    s.config.Exec.Describe,
		stdout:     stdout,
		diagnostic: true,
	})
	if err != nil {
		return client.DescriptionNotFound, fmt.Errorf("describe: %w", err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (s *machineClient) Delete(ctx context.Context, options client.DeleteOptions) error {
	return status.RunStep(
		ctx,
		status.PhaseMachineDelete,
		"delete machine",
		func(ctx context.Context) error {
			return s.delete(ctx, options)
		},
	)
}

func (s *machineClient) delete(ctx context.Context, options client.DeleteOptions) error {
	if options.GracePeriod != "" {
		if duration, err := time.ParseDuration(options.GracePeriod); err == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, duration)
			defer cancel()
		}
	}

	writer := log.Writer(slog.LevelInfo)
	defer func() { _ = writer.Close() }()

	err := s.executor.execute(ctx, execConfig{
		name:         "delete",
		command:      s.config.Exec.Delete,
		stdout:       writer,
		diagnostic:   true,
		withProgress: true,
		startMsg:     "deleting machine",
		doneMsg:      "deleted machine",
	})

	if err != nil && !options.Force {
		return err
	}
	if err != nil {
		log.Errorf("failed to delete machine: machineId=%s, err=%v", s.machine.ID, err)
	}

	return DeleteMachineFolder(s.machine.Context, s.machine.ID)
}

func scheduleLogMessage(msg string) chan struct{} {
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(time.Second * 5):
				log.Info(msg)
			}
		}
	}()

	return done
}
