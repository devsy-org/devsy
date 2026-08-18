package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/devsy-org/devsy/pkg/types"
	"go.uber.org/zap/zapcore"
)

func NewCustomDriver(workspaceInfo *provider.AgentWorkspaceInfo) driver.Driver {
	return &customDriver{
		workspaceInfo: workspaceInfo,
	}
}

var _ driver.Driver = (*customDriver)(nil)

type customDriver struct {
	workspaceInfo *provider.AgentWorkspaceInfo
}

// FindDevContainer returns a running devcontainer details.
func (c *customDriver) FindDevContainer(
	ctx context.Context,
	workspaceId string,
) (*config.ContainerDetails, error) {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	stdout := &bytes.Buffer{}
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceId,
		name:        "findDevContainer",
		command:     c.workspaceInfo.Agent.Custom.FindDevContainer,
		stdout:      stdout,
		stderr:      writer,
	}); err != nil {
		return nil, fmt.Errorf("error finding devcontainer: %s%w", stdout.String(), err)
	} else if len(stdout.Bytes()) == 0 {
		return nil, nil
	}
	var containerDetails config.ContainerDetails
	if err := json.Unmarshal(stdout.Bytes(), &containerDetails); err != nil {
		return nil, fmt.Errorf("error parsing devcontainer details: %s%w", stdout.String(), err)
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
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	stdout := &bytes.Buffer{}
	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceId,
		name:        "getTargetArchitecture",
		command:     c.workspaceInfo.Agent.Custom.TargetArchitecture,
		stdout:      stdout,
		stderr:      writer,
	}); err != nil {
		return "", fmt.Errorf("error getting target architecture: %s%w", stdout.String(), err)
	}

	targetArchitecture := strings.ToLower(strings.TrimSpace(stdout.String()))
	if targetArchitecture != "amd64" && targetArchitecture != "arm64" {
		return "", fmt.Errorf(
			"invalid target architecture %s, expected either arm64 or amd64",
			targetArchitecture,
		)
	}

	return targetArchitecture, nil
}

// DeleteDevContainer deletes the devcontainer.
func (c *customDriver) DeleteDevContainer(ctx context.Context, workspaceId string) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceId,
		name:        "deleteDevContainer",
		command:     c.workspaceInfo.Agent.Custom.DeleteDevContainer,
		stdin:       nil,
		stdout:      writer,
		stderr:      writer,
		extraEnv:    nil,
	}); err != nil {
		return fmt.Errorf("error deleting devcontainer: %w", err)
	}
	return nil
}

// StartDevContainer starts the devcontainer.
func (c *customDriver) StartDevContainer(ctx context.Context, workspaceId string) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceId,
		name:        "startDevContainer",
		command:     c.workspaceInfo.Agent.Custom.StartDevContainer,
		stdout:      writer,
		stderr:      writer,
	}); err != nil {
		return fmt.Errorf("error starting devcontainer: %w", err)
	}

	return nil
}

// StopDevContainer stops the devcontainer.
func (c *customDriver) StopDevContainer(ctx context.Context, workspaceId string) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceId,
		name:        "stopDevContainer",
		command:     c.workspaceInfo.Agent.Custom.StopDevContainer,
		stdin:       nil,
		stdout:      writer,
		stderr:      writer,
		extraEnv:    nil,
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

	done := make(chan struct{})
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	go func() {
		scan := scanner.NewScanner(reader)
		for scan.Scan() {
			log.Info(scan.Text())
		}
		done <- struct{}{}
	}()

	if err := c.runCommand(ctx, runCommandOptions{
		workspaceId: workspaceId,
		name:        "runDevContainer",
		command:     c.workspaceInfo.Agent.Custom.RunDevContainer,
		stdin:       nil,
		stdout:      writer,
		stderr:      writer,
		extraEnv: []string{
			"DEVCONTAINER_RUN_OPTIONS=" + string(out),
		},
	}); err != nil {
		_ = writer.Close() // close writer to unblock logging goroutine
		select {
		case <-done:
		case <-time.After(1 * time.Second): // timeout to avoid hanging if logging goroutine is blocked
		}
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
	workspaceId string
	name        string
	command     types.StrArray
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	extraEnv    []string
}

func (c *customDriver) runCommand(
	ctx context.Context,
	runOpts runCommandOptions,
) error {
	if len(runOpts.command) == 0 {
		return nil
	}

	log.Debugf("run %s driver command: %s", runOpts.name, strings.Join(runOpts.command, " "))
	environ, err := ToEnvironWithBinaries(ctx, c.workspaceInfo)
	if err != nil {
		return err
	}
	environ = append(environ, pkgconfig.EnvDevcontainerID+"="+runOpts.workspaceId)
	environ = append(environ, runOpts.extraEnv...)

	if log.Underlying().Core().Enabled(zapcore.DebugLevel) {
		environ = append(environ, pkgconfig.EnvDebug+"="+pkgconfig.BoolTrue)
	}

	return clientimplementation.RunCommand(ctx, clientimplementation.RunCommandOptions{
		Command: runOpts.command,
		Environ: environ,
		Stdin:   runOpts.stdin,
		Stdout:  runOpts.stdout,
		Stderr:  runOpts.stderr,
	})
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
