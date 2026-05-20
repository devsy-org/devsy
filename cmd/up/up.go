package up

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/spf13/cobra"
)

// UpCmd holds the up cmd flags.
type UpCmd struct {
	provider2.CLIOptions
	*flags.GlobalFlags

	Machine string

	ProviderOptions []string

	ConfigureSSH       bool
	GPGAgentForwarding bool
	OpenIDE            bool
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
}

// NewUpCmd creates a new up command.
func NewUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{GlobalFlags: f}
	upCmd := &cobra.Command{
		Use:   "up [flags] [workspace-path|workspace-name]",
		Short: "Starts a new workspace",
		RunE:  cmd.execute,
	}
	cmd.registerFlags(upCmd)
	return upCmd
}

// Run runs the command logic.
func (cmd *UpCmd) Run( //nolint:cyclop
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	args []string,
) error {
	cmd.prepareWorkspace(client)

	emitJSON := output.ResolveMode(cmd.ResultFormat) == output.ModeJSON

	wctx, err := cmd.executeDevsyUp(ctx, devsyConfig, client)
	if err != nil {
		if emitJSON {
			_ = config2.WriteErrorJSON(os.Stdout, err.Error())
		}
		return err
	}
	if wctx == nil {
		return nil // Platform mode
	}

	if cmd.Prebuild {
		return nil
	}

	if err := cmd.configureWorkspace(devsyConfig, client, wctx); err != nil {
		if emitJSON {
			_ = config2.WriteErrorJSON(os.Stdout, err.Error())
		}
		return err
	}

	if err := cmd.openIDE(ctx, devsyConfig, client, wctx); err != nil {
		if emitJSON {
			_ = config2.WriteErrorJSON(os.Stdout, err.Error())
		}
		return err
	}

	if emitJSON {
		containerID := ""
		var warnings []string
		if wctx.result != nil {
			if wctx.result.ContainerDetails != nil {
				containerID = wctx.result.ContainerDetails.ID
			}
			warnings = wctx.result.HostWarnings
		}
		_ = config2.WriteResultJSON(os.Stdout, containerID, wctx.user, wctx.workdir, warnings)
	}
	return nil
}

func (cmd *UpCmd) execute(cobraCmd *cobra.Command, args []string) error {
	if err := cmd.validate(); err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return fmt.Errorf("load devsy config: %w", err)
	}
	if devsyConfig.ContextOption(config.ContextOptionSSHStrictHostKeyChecking) == config.BoolTrue {
		cmd.StrictHostKeyChecking = true
	}

	cmd.resolveDotfilesOptions(devsyConfig)

	ctx, cancel := WithSignals(cobraCmd.Context())
	defer cancel()

	client, err := cmd.prepareClient(ctx, devsyConfig, args)
	if err != nil {
		return fmt.Errorf("prepare workspace client: %w", err)
	}
	if cmd.ExtraDevContainerPath != "" && client.Provider() != "docker" {
		return fmt.Errorf("extra devcontainer file is only supported with local provider")
	}

	telemetry.CollectorCLI.SetClient(client)
	return cmd.Run(ctx, devsyConfig, client, args)
}

// workspaceContext holds the result of workspace preparation.
type workspaceContext struct {
	result  *config2.Result
	user    string
	workdir string
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

	targetIDE := client.WorkspaceConfig().IDE.Name
	if cmd.IDE != "" {
		targetIDE = cmd.IDE
	}

	if !cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		cmd.SSHAuthSockID = util.RandStringBytes(10)
		log.Debug("Reusing SSH_AUTH_SOCK", cmd.SSHAuthSockID)
	} else if cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		log.Debug(
			"Reusing SSH_AUTH_SOCK is not supported with platform mode, consider launching the IDE from the platform UI",
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
	if err != nil {
		return nil, fmt.Errorf("start workspace: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("did not receive a result back from agent")
	}
	if cmd.Platform.Enabled {
		return nil, nil
	}

	user := config2.GetRemoteUser(result)
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

	return &workspaceContext{result: result, user: user, workdir: workdir}, nil
}

func WithSignals(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	go func() {
		<-ctx.Done()
		<-signals
		// force shutdown if context is done and we receive another signal
		os.Exit(1)
	}()

	return ctx, func() {
		cancel()
		signal.Stop(signals)
	}
}
