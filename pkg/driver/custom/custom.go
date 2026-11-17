package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/types"
)

func NewCustomDriver(workspaceInfo *provider.AgentWorkspaceInfo) driver.Driver {
	return &customDriver{
		workspaceInfo: workspaceInfo,
	}
}

var (
	_ driver.MountDeliveryDriver  = (*customDriver)(nil)
	_ driver.RecreatePolicyDriver = (*customDriver)(nil)
	_ driver.WorkspaceChowner     = (*customDriver)(nil)
)

type customDriver struct {
	workspaceInfo *provider.AgentWorkspaceInfo
}

// FindDevContainer returns a running devcontainer details.
func (c *customDriver) FindDevContainer(
	ctx context.Context,
	workspaceId string,
) (*config.ContainerDetails, error) {
	stdout := &bytes.Buffer{}
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId:      workspaceId,
		name:             "findDevContainer",
		command:          c.workspaceInfo.Agent.Custom.FindDevContainer,
		stdout:           stdout,
		diagnosticStderr: true,
	}); err != nil {
		return nil, fmt.Errorf("error finding devcontainer: %w", err)
	} else if len(stdout.Bytes()) == 0 {
		return nil, nil
	}
	var containerDetails config.ContainerDetails
	if err := json.Unmarshal(stdout.Bytes(), &containerDetails); err != nil {
		return nil, fmt.Errorf("error parsing devcontainer details: %w", err)
	}

	return &containerDetails, nil
}

// CommandDevContainer runs the given command inside the devcontainer.
func (c *customDriver) CommandDevContainer(
	ctx context.Context,
	params *driver.CommandParams,
) error {
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: params.WorkspaceID,
		name:        "commandDevContainer",
		command:     c.workspaceInfo.Agent.Custom.CommandDevContainer,
		stdin:       params.Stdin,
		stdout:      params.Stdout,
		stderr:      params.Stderr,
		rawStdout:   params.RawStdout,
		extraEnv: []string{
			"DEVCONTAINER_USER=" + params.User,
			"DEVCONTAINER_COMMAND=" + params.Command,
		},
	}); err != nil {
		return fmt.Errorf("error running command in devcontainer: %w", err)
	}

	return nil
}

// TargetArchitecture returns the architecture of the container runtime. e.g. amd64 or arm64.
func (c *customDriver) TargetArchitecture(ctx context.Context, workspaceId string) (string, error) {
	stdout := &bytes.Buffer{}
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId:      workspaceId,
		name:             "getTargetArchitecture",
		command:          c.workspaceInfo.Agent.Custom.TargetArchitecture,
		stdout:           stdout,
		diagnosticStderr: true,
	}); err != nil {
		return "", fmt.Errorf("error getting target architecture: %w", err)
	}

	targetArchitecture := strings.ToLower(strings.TrimSpace(stdout.String()))
	if targetArchitecture != "amd64" && targetArchitecture != "arm64" {
		return "", fmt.Errorf("invalid target architecture, expected either arm64 or amd64")
	}

	return targetArchitecture, nil
}

// DeleteDevContainer deletes the devcontainer.
func (c *customDriver) DeleteDevContainer(ctx context.Context, workspaceId string) error {
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId:      workspaceId,
		name:             "deleteDevContainer",
		command:          c.workspaceInfo.Agent.Custom.DeleteDevContainer,
		stdin:            nil,
		diagnosticStdout: true,
		diagnosticStderr: true,
		extraEnv:         nil,
	}); err != nil {
		return fmt.Errorf("error deleting devcontainer: %w", err)
	}
	return nil
}

// StartDevContainer starts the devcontainer.
func (c *customDriver) StartDevContainer(ctx context.Context, workspaceId string) error {
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId:      workspaceId,
		name:             "startDevContainer",
		command:          c.workspaceInfo.Agent.Custom.StartDevContainer,
		diagnosticStdout: true,
		diagnosticStderr: true,
	}); err != nil {
		return fmt.Errorf("error starting devcontainer: %w", err)
	}

	return nil
}

// StopDevContainer stops the devcontainer.
func (c *customDriver) StopDevContainer(ctx context.Context, workspaceId string) error {
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId:      workspaceId,
		name:             "stopDevContainer",
		command:          c.workspaceInfo.Agent.Custom.StopDevContainer,
		stdin:            nil,
		diagnosticStdout: true,
		diagnosticStderr: true,
		extraEnv:         nil,
	}); err != nil {
		return fmt.Errorf("error stopping devcontainer: %w", err)
	}

	return nil
}

// RunDevContainer runs a devcontainer.
func (c *customDriver) RunDevContainer(
	ctx context.Context,
	workspaceId string,
	options *driver.RunOptions,
) error {
	out, err := json.Marshal(options)
	if err != nil {
		return fmt.Errorf("marshal run options: %w", err)
	}

	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId:      workspaceId,
		name:             "runDevContainer",
		command:          c.workspaceInfo.Agent.Custom.RunDevContainer,
		stdin:            nil,
		diagnosticStdout: true,
		diagnosticStderr: true,
		extraEnv: []string{
			"DEVCONTAINER_RUN_OPTIONS=" + string(out),
		},
	}); err != nil {
		return fmt.Errorf("error running devcontainer: %w", err)
	}

	return nil
}

func (c *customDriver) GetDevContainerLogs(
	ctx context.Context,
	workspaceID string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceID,
		name:        "getDevContainerLogs",
		command:     c.workspaceInfo.Agent.Custom.GetDevContainerLogs,
		stdout:      stdout,
		stderr:      stderr,
	}); err != nil {
		return fmt.Errorf("error getting devcontainer logs: %w", err)
	}

	return nil
}

var _ driver.ReprovisioningDriver = (*customDriver)(nil)

func (c *customDriver) CanReprovision() bool {
	return c.workspaceInfo.Agent.Custom.CanReprovision == pkgconfig.BoolTrue
}

type runCommandOptions struct {
	workspaceId      string
	name             string
	command          types.StrArray
	stdin            io.Reader
	stdout           io.Writer
	stderr           io.Writer
	rawStdout        bool
	extraEnv         []string
	diagnosticStdout bool
	diagnosticStderr bool
}

func ToEnvironWithBinaries(
	ctx context.Context,
	workspace *provider.AgentWorkspaceInfo,
) ([]string, error) {
	// get binaries dir
	binariesDir, err := agent.GetAgentBinariesDirFromWorkspaceDir(workspace.Origin)
	if err != nil {
		return nil, fmt.Errorf(
			"error getting workspace %s binaries dir: %s: %w",
			workspace.Workspace.ID, workspace.Origin, err,
		)
	}

	agentBinaries, err := provider.DownloadBinaries(ctx, workspace.Agent.Binaries, binariesDir)
	if err != nil {
		return nil, fmt.Errorf(
			"error downloading workspace %s binaries: %w",
			workspace.Workspace.ID,
			err,
		)
	}

	environ := provider.ToEnvironment(
		workspace.Workspace,
		workspace.Machine,
		workspace.Options,
		nil,
	)
	for k, v := range agentBinaries {
		environ = append(environ, k+"="+v)
	}

	return environ, nil
}

func (d *customDriver) RequiresMountStreaming() bool { return true }

func (d *customDriver) RecreateMode() driver.RecreateMode { return driver.RecreateStop }

func (d *customDriver) RequiresWorkspaceChown() bool { return true }

func (c *customDriver) runCommand(
	ctx context.Context,
	runOpts runCommandOptions,
) error {
	if len(runOpts.command) == 0 {
		return nil
	}

	log.Debugf("run %s driver command", runOpts.name)
	environ, err := ToEnvironWithBinaries(ctx, c.workspaceInfo)
	if err != nil {
		return err
	}
	environ = append(environ, pkgconfig.EnvDevcontainerID+"="+runOpts.workspaceId)
	environ = append(environ, runOpts.extraEnv...)

	var stdoutStream, stderrStream *log.JSONLogStreamer
	if runOpts.diagnosticStdout || runOpts.diagnosticStderr {
		var optionEntries []string
		for key, value := range provider.CombineOptions(
			c.workspaceInfo.Workspace, c.workspaceInfo.Machine, c.workspaceInfo.Options,
		) {
			optionEntries = append(optionEntries, key+"="+value.Value)
		}
		attrs := []slog.Attr{
			log.AttrComponent("custom-driver"),
			log.AttrOperation(runOpts.name),
			log.AttrWorkspaceID(runOpts.workspaceId),
		}
		if c.workspaceInfo.Workspace != nil {
			attrs = append(attrs, log.AttrProvider(c.workspaceInfo.Workspace.Provider.Name))
		}
		if c.workspaceInfo.Machine != nil {
			attrs = append(attrs, log.AttrMachineID(c.workspaceInfo.Machine.ID))
		}
		streamOptions := log.StreamerOptions{
			Redactor: secrets.Combine(
				secrets.NewRedactor(optionEntries),
				secrets.NewEnvironmentRedactor(environ),
			),
			FallbackLevel: log.SlogLevelInfo,
			DefaultAttrs:  attrs,
		}
		if runOpts.diagnosticStdout {
			stdoutStream = log.NewJSONLogStreamer(streamOptions)
			runOpts.stdout = stdoutStream
		}
		if runOpts.diagnosticStderr {
			streamOptions.CaptureLines = 50
			streamOptions.CaptureBytes = 32 * 1024
			stderrStream = log.NewJSONLogStreamer(streamOptions)
			runOpts.stderr = stderrStream
		}
	}

	err = clientimplementation.RunCommand(ctx, clientimplementation.RunCommandOptions{
		Command:   runOpts.command,
		Environ:   environ,
		Stdin:     runOpts.stdin,
		Stdout:    runOpts.stdout,
		Stderr:    runOpts.stderr,
		RawStdout: runOpts.rawStdout,
	})
	if stdoutStream != nil {
		_ = stdoutStream.Close()
	}
	if stderrStream != nil {
		_ = stderrStream.Close()
		if output := stderrStream.ErrorOutput(); err != nil && output != "" {
			err = fmt.Errorf("%s: %w", output, err)
		}
	}
	return err
}
