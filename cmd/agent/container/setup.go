//go:build !windows

package container

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
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/ide/fleet"
	"github.com/devsy-org/devsy/pkg/ide/jetbrains"
	"github.com/devsy-org/devsy/pkg/ide/jupyter"
	"github.com/devsy-org/devsy/pkg/ide/openvscode"
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
}

// NewSetupContainerCmd creates a new command.
func NewSetupContainerCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupContainerCmd{
		GlobalFlags: flags,
	}
	setupContainerCmd := &cobra.Command{
		Use:   "setup",
		Short: "Sets up a container",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	setupContainerCmd.Flags().
		BoolVar(&cmd.StreamMounts, "stream-mounts", false, "If true, will try to stream the bind mounts from the host")
	setupContainerCmd.Flags().
		BoolVar(&cmd.Prebuild, "prebuild", false,
			"If true, only run prebuild lifecycle hooks (onCreateCommand + updateContentCommand)")
	setupContainerCmd.Flags().
		BoolVar(&cmd.ChownWorkspace, "chown-workspace", false, "If Devsy should chown the workspace to the remote user")
	setupContainerCmd.Flags().
		BoolVar(&cmd.InjectGitCredentials, "inject-git-credentials", false,
			"If Devsy should inject git credentials during setup")
	setupContainerCmd.Flags().
		StringVar(&cmd.ContainerWorkspaceInfo, "container-workspace-info", "", "The container workspace info")
	setupContainerCmd.Flags().
		StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	setupContainerCmd.Flags().StringVar(&cmd.AccessKey, "access-key", "", "Access Key to use")
	setupContainerCmd.Flags().
		StringVar(&cmd.WorkspaceHost, "workspace-host", "", "Workspace hostname to use")
	setupContainerCmd.Flags().StringVar(&cmd.PlatformHost, "platform-host", "", "Platform host")
	_ = setupContainerCmd.MarkFlagRequired("setup-info")
	return setupContainerCmd
}

type setupContext struct {
	ctx           context.Context
	workspaceInfo *provider2.ContainerWorkspaceInfo
	setupInfo     *config.Result
	tunnelClient  tunnel.TunnelClient
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

	sctx := &setupContext{
		ctx:           ctx,
		workspaceInfo: workspaceInfo,
		setupInfo:     setupInfo,
		tunnelClient:  tunnelClient,
	}

	if err := cmd.prepareWorkspace(sctx); err != nil {
		return err
	}

	return cmd.finalizeSetup(sctx)
}

func (cmd *SetupContainerCmd) prepareWorkspace(sctx *setupContext) error {
	if err := cmd.syncMounts(sctx); err != nil {
		return err
	}

	if err := agent.DockerlessBuild(agent.DockerlessBuildOptions{
		Context:           sctx.ctx,
		SetupInfo:         sctx.setupInfo,
		DockerlessOptions: &sctx.workspaceInfo.Dockerless,
		ImageConfigOutput: agent.DefaultImageConfigPath,
		Debug:             cmd.Debug,
		ConfigureCredentialsFunc: func(ctx context.Context) (string, error) {
			serverPort, err := credentials.StartCredentialsServer(
				ctx,
				sctx.tunnelClient,
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

	if err := fillContainerEnv(sctx.setupInfo); err != nil {
		return err
	}

	cleanupFunc := cmd.setupGitCredentials(
		sctx.ctx,
		sctx.tunnelClient,
	)

	// Clone repository before cleaning up git credentials
	cloneErr := cmd.cloneRepositoryIfNeeded(
		sctx.ctx,
		sctx.workspaceInfo,
		sctx.setupInfo,
	)

	// Clean up git credentials after cloning
	if cleanupFunc != nil {
		cleanupFunc()
	}

	return cloneErr
}

func (cmd *SetupContainerCmd) finalizeSetup(sctx *setupContext) error {
	cfg := &setup.ContainerSetupConfig{
		SetupInfo:         sctx.setupInfo,
		ExtraWorkspaceEnv: sctx.workspaceInfo.CLIOptions.WorkspaceEnv,
		ChownProjects:     cmd.ChownWorkspace,
		PlatformOptions:   &sctx.workspaceInfo.CLIOptions.Platform,
		TunnelClient:      sctx.tunnelClient,
		Prebuild:          cmd.Prebuild,
	}

	if err := setup.SetupContainerPreAttach(sctx.ctx, cfg); err != nil {
		return err
	}

	if !cmd.Prebuild {
		if err := cmd.installIDE(sctx.setupInfo, &sctx.workspaceInfo.IDE); err != nil {
			return err
		}

		if err := cmd.startContainerDaemon(sctx.workspaceInfo); err != nil {
			return err
		}

		// Launch postAttachCommand as a detached background process before sending
		// the result. Once sendSetupResult returns, the client tears down the SSH
		// tunnel which kills this process, so postAttach must already be running
		// independently.
		if err := cmd.startPostAttachHooks(sctx); err != nil {
			log.Errorf("failed to start postAttachCommand: %v", err)
		}
	}

	return cmd.sendSetupResult(sctx.ctx, sctx.setupInfo, sctx.tunnelClient)
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

func (cmd *SetupContainerCmd) syncMounts(sctx *setupContext) error {
	if !cmd.StreamMounts {
		return nil
	}

	mounts := config.GetMounts(sctx.setupInfo)
	log.Debugf("syncing mounts: %v", mounts)
	for _, m := range mounts {
		if !sctx.workspaceInfo.CLIOptions.Reset {
			files, err := os.ReadDir(m.Target)
			if err == nil && len(files) > 0 {
				log.Debugf("skip stream mount %s because it is not empty", m.Target)
				continue
			}
		}

		if err := streamMount(
			sctx.ctx,
			sctx.workspaceInfo,
			m,
			sctx.tunnelClient,
		); err != nil {
			return err
		}
	}

	return nil
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

	return agent.CloneRepositoryForWorkspace(ctx,
		&workspaceInfo.Source,
		&workspaceInfo.Agent,
		setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
		"",
		workspaceInfo.CLIOptions,
		true,
	)
}

func (cmd *SetupContainerCmd) startContainerDaemon(
	workspaceInfo *provider2.ContainerWorkspaceInfo,
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

		//nolint:gosec // binaryPath is from os.Executable(), not user input
		return exec.Command(
			binaryPath,
			"agent",
			"container",
			"daemon",
			"--timeout",
			workspaceInfo.ContainerTimeout,
		), nil
	})
}

func (cmd *SetupContainerCmd) startPostAttachHooks(sctx *setupContext) error {
	if len(sctx.setupInfo.MergedConfig.PostAttachCommands) == 0 {
		return nil
	}

	return command.StartBackgroundOnce("devsy.post-attach", func() (*exec.Cmd, error) {
		log.Debugf("starting postAttachCommand as background process")
		binaryPath, err := os.Executable()
		if err != nil {
			return nil, err
		}

		//nolint:gosec // binaryPath is from os.Executable(), not user input
		return exec.Command(
			binaryPath,
			"agent",
			"container",
			"post-attach",
			"--setup-info",
			cmd.SetupInfo,
		), nil
	})
}

func (cmd *SetupContainerCmd) sendSetupResult(
	ctx context.Context,
	setupInfo *config.Result,
	tunnelClient tunnel.TunnelClient,
) error {
	out, err := json.Marshal(setupInfo)
	if err != nil {
		return fmt.Errorf("marshal setup info: %w", err)
	}

	if _, err := tunnelClient.SendResult(ctx, &tunnel.Message{Message: string(out)}); err != nil {
		return fmt.Errorf("send result: %w", err)
	}

	return nil
}

func fillContainerEnv(setupInfo *config.Result) error {
	// set remote-env
	if setupInfo.MergedConfig.RemoteEnv == nil {
		setupInfo.MergedConfig.RemoteEnv = make(map[string]string)
	}

	if _, ok := setupInfo.MergedConfig.RemoteEnv["PATH"]; !ok {
		setupInfo.MergedConfig.RemoteEnv["PATH"] = "${containerEnv:PATH}"
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

func (cmd *SetupContainerCmd) installIDE(
	setupInfo *config.Result,
	ide *provider2.WorkspaceIDEConfig,
) error {
	switch ide.Name {
	case string(config2.IDENone):
		return nil
	case string(config2.IDEVSCode):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorStable)
	case string(config2.IDEVSCodeInsiders):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorInsiders)
	case string(config2.IDECursor):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorCursor)
	case string(config2.IDEPositron):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorPositron)
	case string(config2.IDECodium):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorCodium)
	case string(config2.IDEWindsurf):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorWindsurf)
	case string(config2.IDEAntigravity):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorAntigravity)
	case string(config2.IDEBob):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorBob)
	case string(config2.IDEOpenVSCode):
		return cmd.setupOpenVSCode(setupInfo, ide.Options)
	case string(config2.IDEGoland):
		return jetbrains.NewGolandServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDERustRover):
		return jetbrains.NewRustRoverServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDEPyCharm):
		return jetbrains.NewPyCharmServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDEPhpStorm):
		return jetbrains.NewPhpStorm(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDEIntellij):
		return jetbrains.NewIntellij(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDECLion):
		return jetbrains.NewCLionServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDERider):
		return jetbrains.NewRiderServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDERubyMine):
		return jetbrains.NewRubyMineServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDEWebStorm):
		return jetbrains.NewWebStormServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDEDataSpell):
		return jetbrains.NewDataSpellServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo)
	case string(config2.IDEFleet):
		return fleet.NewFleetServer(config.GetRemoteUser(setupInfo), ide.Options).
			Install(setupInfo.SubstitutionContext.ContainerWorkspaceFolder)
	case string(config2.IDEJupyterNotebook):
		return jupyter.NewJupyterNotebookServer(
			setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
			config.GetRemoteUser(setupInfo), ide.Options).
			Install()
	case string(config2.IDERStudio):
		return rstudio.NewRStudioServer(
			setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
			config.GetRemoteUser(setupInfo), ide.Options).
			Install()
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
				"agent", "container", "vscode-async",
				"--setup-info", cmd.SetupInfo,
				"--flavor", string(flavor),
			}

			//nolint:gosec // binaryPath is from os.Executable(), not user input
			return exec.Command(binaryPath, args...), nil
		})
}

func (cmd *SetupContainerCmd) setupOpenVSCode(
	setupInfo *config.Result,
	ideOptions map[string]config2.OptionValue,
) error {
	log.Debugf("setup openvscode")
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	settings := ""
	if len(vsCodeConfiguration.Settings) > 0 {
		out, err := json.Marshal(vsCodeConfiguration.Settings)
		if err != nil {
			return err
		}

		settings = string(out)
	}

	user := config.GetRemoteUser(setupInfo)
	openVSCode := openvscode.NewOpenVSCodeServer(
		vsCodeConfiguration.Extensions,
		settings,
		user,
		"0.0.0.0",
		strconv.Itoa(openvscode.DefaultVSCodePort),
		ideOptions,
	)

	// install open vscode
	err := openVSCode.Install()
	if err != nil {
		return err
	}

	// install extensions in background
	if len(vsCodeConfiguration.Extensions) > 0 {
		err = command.StartBackgroundOnce("openvscode-async", func() (*exec.Cmd, error) {
			log.Infof(
				"installing extensions in the background: %s",
				strings.Join(vsCodeConfiguration.Extensions, ","),
			)
			binaryPath, err := os.Executable()
			if err != nil {
				return nil, err
			}

			return exec.Command(
				binaryPath,
				"agent",
				"container",
				"openvscode-async",
				"--setup-info",
				cmd.SetupInfo,
			), nil
		})
		if err != nil {
			return fmt.Errorf("install extensions: %w", err)
		}
	}

	// start the server in the background
	return openVSCode.Start()
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

	gitCredentials := fmt.Sprintf("!'%s' agent git-credentials --port %d", binaryPath, serverPort)
	_ = os.Setenv(config2.EnvGitHelperPort, strconv.Itoa(serverPort))

	err = git.CommandContext(ctx, git.GetDefaultExtraEnv(false), "config", "--system", "--add",
		"credential.helper", gitCredentials).
		Run()
	if err != nil {
		return nil, fmt.Errorf("add git credential helper: %w", err)
	}

	cleanup := func() {
		log.Debug("unset setup system credential helper")
		err = git.CommandContext(ctx, git.GetDefaultExtraEnv(false), "config", "--system", "--unset", "credential.helper").
			Run()
		if err != nil {
			log.Errorf("unset system credential helper %v", err)
		}
	}

	return cleanup, nil
}

func streamMount(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
	tunnelClient tunnel.TunnelClient,
) error {
	// if we have a platform workspace socket we connect directly to it
	if workspaceInfo.CLIOptions.Platform.Enabled {
		// check if the runner proxy socket exists
		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}

		// build the url
		log.Infof("Download %s into DevContainer %s", m.Source, m.Target)
		url := fmt.Sprintf(
			"https://%s/kubernetes/management/apis/management.devsy.sh/v1/namespaces/%s/devsyworkspaceinstances/%s/download?path=%s",
			ts.RemoveProtocol(workspaceInfo.CLIOptions.Platform.PlatformHost),
			workspaceInfo.CLIOptions.Platform.InstanceNamespace,
			workspaceInfo.CLIOptions.Platform.InstanceName,
			url.QueryEscape(m.Source),
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set(
			"Authorization",
			fmt.Sprintf("Bearer %s", workspaceInfo.CLIOptions.Platform.AccessKey),
		)

		// send the request
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

		// create progress reader
		progressReader := &progressReader{
			Reader: resp.Body,
		}

		// target folder
		err = extract.Extract(progressReader, m.Target)
		if err != nil {
			return fmt.Errorf("stream mount %s: %w", m.String(), err)
		}

		return nil
	}

	// stream mount
	log.Infof("Copy %s into DevContainer %s", m.Source, m.Target)
	stream, err := tunnelClient.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: m.String()})
	if err != nil {
		return fmt.Errorf("init stream mount %s: %w", m.String(), err)
	}

	// target folder
	err = extract.Extract(tunnelserver.NewStreamReader(stream), m.Target)
	if err != nil {
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
