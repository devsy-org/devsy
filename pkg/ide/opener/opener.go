package opener

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/ide/fleet"
	"github.com/devsy-org/devsy/pkg/ide/jetbrains"
	"github.com/devsy-org/devsy/pkg/ide/jupyter"
	"github.com/devsy-org/devsy/pkg/ide/openvscode"
	"github.com/devsy-org/devsy/pkg/ide/rstudio"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	"github.com/devsy-org/devsy/pkg/ide/zed"
	pkglog "github.com/devsy-org/devsy/pkg/log"
	open2 "github.com/devsy-org/devsy/pkg/open"
	"github.com/devsy-org/devsy/pkg/port"
	"github.com/devsy-org/devsy/pkg/tunnel"
)

// IDEParams holds the parameters needed to open an IDE.
type IDEParams struct {
	GPGAgentForwarding bool
	SSHAuthSockID      string
	GitSSHSigningKey   string
	DevsyConfig        *config.Config
	Client             client2.BaseWorkspaceClient
	User               string
	Result             *config2.Result
	TunnelMode         bool
	Launch             IDELaunchMode
}

// IsBrowserIDE reports whether the given IDE name uses a browser-based
// tunnel (openvscode, jupyter, rstudio). These IDEs spawn a detached
// helper process for the tunnel; the CLI does not block on the IDE
// session lifetime.
func IsBrowserIDE(ideName string) bool {
	_, ok := browserIDEOpener(ideName)
	return ok
}

// Open dispatches to the correct IDE opener based on ideName. It returns the
// IDE URL (when meaningful — see per-IDE openers) along with any error.
func Open(
	ctx context.Context,
	ideName string,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) (string, error) {
	if fn, ok := browserIDEOpener(ideName); ok {
		return fn(ctx, ideOptions, params)
	}

	return openDesktopIDE(ctx, ideName, ideOptions, params)
}

// browserIDEOpener returns a handler for browser-based IDEs if ideName matches.
func browserIDEOpener(
	ideName string,
) (func(context.Context, map[string]config.OptionValue, IDEParams) (string, error), bool) {
	switch ideName {
	case string(config.IDEOpenVSCode):
		return openVSCodeBrowser, true
	case string(config.IDEJupyterNotebook):
		return openJupyterBrowser, true
	case string(config.IDERStudio):
		return openRStudioBrowser, true
	default:
		return nil, false
	}
}

func openDesktopIDE(
	ctx context.Context,
	ideName string,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) (string, error) {
	// Fleet is special: its opener retrieves a workspace-side URL even in
	// headless mode (just doesn't pop a host browser). Route to startFleet
	// before the headless short-circuit so the URL still gets logged.
	if ideName == string(config.IDEFleet) {
		return startFleet(ctx, params)
	}

	// For other desktop IDEs (VSCode flavors, JetBrains, Zed), headless and
	// skip are equivalent at the opener layer: backend install (if any)
	// happens during workspace setup; the opener phase only does the host
	// launch, which headless suppresses.
	if params.Launch == LaunchHeadless {
		return "", nil
	}

	switch ideName {
	case string(config.IDEVSCode), string(config.IDEVSCodeInsiders), string(config.IDECursor),
		string(config.IDECodium), string(config.IDEPositron), string(config.IDEWindsurf),
		string(config.IDEAntigravity), string(config.IDEBob):
		return "", openVSCodeFlavor(ctx, ideName, ideOptions, params)

	case string(config.IDERustRover), string(config.IDEGoland), string(config.IDEPyCharm),
		string(config.IDEPhpStorm), string(config.IDEIntellij), string(config.IDECLion),
		string(config.IDERider), string(config.IDERubyMine), string(config.IDEWebStorm),
		string(config.IDEDataSpell):
		return "", openJetBrains(ideName, ideOptions, params)

	case string(config.IDEZed):
		return "", zed.Open(
			ctx, ideOptions, params.User,
			params.Result.SubstitutionContext.ContainerWorkspaceFolder,
			params.Client.Workspace(),
		)

	default:
		return "", nil
	}
}

// ParseAddressAndPort parses a bind address option into host address and port.
// If bindAddressOption is empty, it finds an available port starting from defaultPort.
func ParseAddressAndPort(bindAddressOption string, defaultPort int) (string, int, error) {
	if bindAddressOption == "" {
		return parseDefaultPort(defaultPort)
	}

	return parseExplicitAddress(bindAddressOption)
}

func parseDefaultPort(defaultPort int) (string, int, error) {
	portName, err := port.FindAvailablePort(defaultPort)
	if err != nil {
		return "", 0, err
	}

	return fmt.Sprintf("%d", portName), portName, nil
}

func parseExplicitAddress(address string) (string, int, error) {
	_, p, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("parse host:port: %w", err)
	}
	if p == "" {
		return "", 0, fmt.Errorf("parse ADDRESS: expected host:port, got %s", address)
	}

	portName, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("parse host:port: %w", err)
	}

	return address, portName, nil
}

var vsCodeFlavorMap = map[string]vscode.Flavor{
	string(config.IDEVSCode):         vscode.FlavorStable,
	string(config.IDEVSCodeInsiders): vscode.FlavorInsiders,
	string(config.IDECursor):         vscode.FlavorCursor,
	string(config.IDECodium):         vscode.FlavorCodium,
	string(config.IDEPositron):       vscode.FlavorPositron,
	string(config.IDEWindsurf):       vscode.FlavorWindsurf,
	string(config.IDEAntigravity):    vscode.FlavorAntigravity,
	string(config.IDEBob):            vscode.FlavorBob,
}

func openVSCodeFlavor(
	ctx context.Context,
	ideName string,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) error {
	return vscode.Open(ctx, vscode.OpenParams{
		Workspace:  params.Client.Workspace(),
		Folder:     params.Result.SubstitutionContext.ContainerWorkspaceFolder,
		NewWindow:  vscode.Options.GetValue(ideOptions, vscode.OpenNewWindow) == config.BoolTrue,
		Flavor:     vsCodeFlavorMap[ideName],
		TunnelMode: params.TunnelMode,
	})
}

func openJetBrains(
	ideName string,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) error {
	folder := params.Result.SubstitutionContext.ContainerWorkspaceFolder
	workspace := params.Client.Workspace()
	user := params.User
	type jetbrainsFactory func() interface{ OpenGateway(string, string) error }

	jetbrainsMap := map[string]jetbrainsFactory{
		string(config.IDERustRover): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewRustRoverServer(user, ideOptions)
		},
		string(config.IDEGoland): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewGolandServer(user, ideOptions)
		},
		string(config.IDEPyCharm): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewPyCharmServer(user, ideOptions)
		},
		string(config.IDEPhpStorm): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewPhpStorm(user, ideOptions)
		},
		string(config.IDEIntellij): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewIntellij(user, ideOptions)
		},
		string(config.IDECLion): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewCLionServer(user, ideOptions)
		},
		string(config.IDERider): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewRiderServer(user, ideOptions)
		},
		string(config.IDERubyMine): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewRubyMineServer(user, ideOptions)
		},
		string(config.IDEWebStorm): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewWebStormServer(user, ideOptions)
		},
		string(config.IDEDataSpell): func() interface{ OpenGateway(string, string) error } {
			return jetbrains.NewDataSpellServer(user, ideOptions)
		},
	}

	if factory, ok := jetbrainsMap[ideName]; ok {
		return factory().OpenGateway(folder, workspace)
	}
	return fmt.Errorf("unknown JetBrains IDE: %s", ideName)
}

func makeDaemonStartFunc(
	params IDEParams,
	forwardPorts bool,
	extraPorts []string,
) func(ctx context.Context) error {
	daemonClient, ok := params.Client.(client2.DaemonClient)
	if !ok {
		return nil
	}

	return func(ctx context.Context) error {
		toolClient, _, err := daemonClient.SSHClients(ctx, params.User)
		if err != nil {
			return err
		}
		defer func() { _ = toolClient.Close() }()

		err = clientimplementation.StartServicesDaemon(
			ctx,
			clientimplementation.StartServicesDaemonOptions{
				DevsyConfig:      params.DevsyConfig,
				Client:           daemonClient,
				SSHClient:        toolClient,
				User:             params.User,
				ForwardPorts:     forwardPorts,
				ExtraPorts:       extraPorts,
				GitSSHSigningKey: params.GitSSHSigningKey,
			},
		)
		if err != nil {
			return err
		}
		<-ctx.Done()

		return nil
	}
}

func openJupyterBrowser(
	ctx context.Context,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) (string, error) {
	if params.GPGAgentForwarding {
		if err := gpg.ForwardAgent(params.Client); err != nil {
			return "", err
		}
	}

	addr, jupyterPort, err := ParseAddressAndPort(
		jupyter.Options.GetValue(ideOptions, jupyter.BindAddressOption),
		jupyter.DefaultServerPort,
	)
	if err != nil {
		return "", err
	}

	targetURL := fmt.Sprintf("http://localhost:%d/lab", jupyterPort)
	openBrowser := params.Launch == LaunchAuto

	pkglog.Infof("Starting jupyter notebook in browser mode at %s", targetURL)
	extraPorts := []string{fmt.Sprintf("%s:%d", addr, jupyter.DefaultServerPort)}
	if err := startDetachedBrowserTunnel(ctx, params, tunnel.BrowserTunnelParams{
		DevsyConfig:      params.DevsyConfig,
		Client:           params.Client,
		User:             params.User,
		TargetURL:        targetURL,
		ExtraPorts:       extraPorts,
		AuthSockID:       params.SSHAuthSockID,
		GitSSHSigningKey: params.GitSSHSigningKey,
		DaemonStartFunc:  makeDaemonStartFunc(params, false, extraPorts),
	}, browserIDEInvocation{Label: "jupyter", OpenBrowser: openBrowser}); err != nil {
		return "", err
	}
	return targetURL, nil
}

func openRStudioBrowser(
	ctx context.Context,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) (string, error) {
	if params.GPGAgentForwarding {
		if err := gpg.ForwardAgent(params.Client); err != nil {
			return "", err
		}
	}

	addr, rsPort, err := ParseAddressAndPort(
		rstudio.Options.GetValue(ideOptions, rstudio.BindAddressOption),
		rstudio.DefaultServerPort,
	)
	if err != nil {
		return "", err
	}

	targetURL := fmt.Sprintf("http://localhost:%d", rsPort)
	openBrowser := params.Launch == LaunchAuto

	pkglog.Infof("Starting RStudio server in browser mode at %s", targetURL)
	extraPorts := []string{fmt.Sprintf("%s:%d", addr, rstudio.DefaultServerPort)}
	if err := startDetachedBrowserTunnel(ctx, params, tunnel.BrowserTunnelParams{
		DevsyConfig:      params.DevsyConfig,
		Client:           params.Client,
		User:             params.User,
		TargetURL:        targetURL,
		ExtraPorts:       extraPorts,
		AuthSockID:       params.SSHAuthSockID,
		GitSSHSigningKey: params.GitSSHSigningKey,
		DaemonStartFunc:  makeDaemonStartFunc(params, false, extraPorts),
	}, browserIDEInvocation{Label: "rstudio", OpenBrowser: openBrowser}); err != nil {
		return "", err
	}
	return targetURL, nil
}

func openVSCodeBrowser(
	ctx context.Context,
	ideOptions map[string]config.OptionValue,
	params IDEParams,
) (string, error) {
	if params.GPGAgentForwarding {
		if err := gpg.ForwardAgent(params.Client); err != nil {
			return "", err
		}
	}

	folder := params.Result.SubstitutionContext.ContainerWorkspaceFolder
	addr, vscodePort, err := ParseAddressAndPort(
		openvscode.Options.GetValue(ideOptions, openvscode.BindAddressOption),
		openvscode.DefaultVSCodePort,
	)
	if err != nil {
		return "", err
	}

	targetURL := fmt.Sprintf("http://localhost:%d/?folder=%s", vscodePort, folder)
	openBrowser := params.Launch == LaunchAuto

	pkglog.Infof("Starting vscode in browser mode at %s", targetURL)
	forwardPorts := openvscode.Options.GetValue(
		ideOptions,
		openvscode.ForwardPortsOption,
	) == config.BoolTrue
	extraPorts := []string{fmt.Sprintf("%s:%d", addr, openvscode.DefaultVSCodePort)}
	if err := startDetachedBrowserTunnel(ctx, params, tunnel.BrowserTunnelParams{
		DevsyConfig:      params.DevsyConfig,
		Client:           params.Client,
		User:             params.User,
		TargetURL:        targetURL,
		ForwardPorts:     forwardPorts,
		ExtraPorts:       extraPorts,
		AuthSockID:       params.SSHAuthSockID,
		GitSSHSigningKey: params.GitSSHSigningKey,
		DaemonStartFunc:  makeDaemonStartFunc(params, forwardPorts, extraPorts),
	}, browserIDEInvocation{Label: LabelVSCodeBrowser, OpenBrowser: openBrowser}); err != nil {
		return "", err
	}
	return targetURL, nil
}

func startFleet(ctx context.Context, params IDEParams) (string, error) {
	stdout := &bytes.Buffer{}
	sshCmd, err := tunnel.CreateSSHCommand(
		ctx,
		params.Client,
		[]string{"--command", "cat " + fleet.FleetURLFileName},
	)
	if err != nil {
		return "", err
	}
	sshCmd.Stdout = stdout
	err = sshCmd.Run()
	if err != nil {
		return "", command.WrapCommandError(stdout.Bytes(), err)
	}

	url := strings.TrimSpace(stdout.String())
	if len(url) == 0 {
		return "", fmt.Errorf("seems like fleet is not running within the container")
	}

	if params.Launch == LaunchHeadless {
		// Headless: surface the URL so the caller can grab it. Skip the
		// public-URL warning since the caller explicitly opted out of the
		// interactive flow.
		pkglog.Infof("Fleet is available at %s", url)
		return url, nil
	}
	pkglog.Warnf(
		"Fleet is exposed at a publicly reachable URL, please make sure to not disclose this URL " +
			"to anyone as they will be able to reach your workspace from that",
	)
	pkglog.Infof("Starting Fleet at %s ...", url)
	if err := open2.Run(url); err != nil {
		return "", err
	}
	return url, nil
}
