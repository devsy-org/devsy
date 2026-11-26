//go:build !windows

package agentcontainer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/fleet"
	"github.com/devsy-org/devsy/pkg/ide/jetbrains"
	"github.com/devsy-org/devsy/pkg/ide/jupyter"
	"github.com/devsy-org/devsy/pkg/ide/marimo"
	"github.com/devsy-org/devsy/pkg/ide/rstudio"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

var vscodeFlavors = map[string]vscode.Flavor{
	string(config2.IDEVSCode):         vscode.FlavorStable,
	string(config2.IDEVSCodeInsiders): vscode.FlavorInsiders,
	string(config2.IDECursor):         vscode.FlavorCursor,
	string(config2.IDEPositron):       vscode.FlavorPositron,
	string(config2.IDECodium):         vscode.FlavorCodium,
	string(config2.IDEWindsurf):       vscode.FlavorWindsurf,
	string(config2.IDEAntigravity):    vscode.FlavorAntigravity,
	string(config2.IDEBob):            vscode.FlavorBob,
}

type jetbrainsServerFactory func(
	string,
	map[string]config2.OptionValue,
) *jetbrains.GenericJetBrainsServer

var jetbrainsServers = map[string]jetbrainsServerFactory{
	string(config2.IDEGoland):    jetbrains.NewGolandServer,
	string(config2.IDERustRover): jetbrains.NewRustRoverServer,
	string(config2.IDEPyCharm):   jetbrains.NewPyCharmServer,
	string(config2.IDEPhpStorm):  jetbrains.NewPhpStorm,
	string(config2.IDEIntellij):  jetbrains.NewIntellij,
	string(config2.IDECLion):     jetbrains.NewCLionServer,
	string(config2.IDERider):     jetbrains.NewRiderServer,
	string(config2.IDERubyMine):  jetbrains.NewRubyMineServer,
	string(config2.IDEWebStorm):  jetbrains.NewWebStormServer,
	string(config2.IDEDataSpell): jetbrains.NewDataSpellServer,
}

func (cmd *SetupContainerCmd) installIDE(
	setupInfo *config.Result,
	ide *provider2.WorkspaceIDEConfig,
) error {
	if flavor, ok := vscodeFlavors[ide.Name]; ok {
		return cmd.setupVSCode(setupInfo, ide.Options, flavor)
	}
	if newServer, ok := jetbrainsServers[ide.Name]; ok {
		return newServer(config.GetRemoteUser(setupInfo), ide.Options).Install(setupInfo)
	}
	switch ide.Name {
	case string(config2.IDENone):
		return nil
	case string(config2.IDEOpenVSCode), string(config2.IDECodeServer), string(config2.IDEVSCodeWeb):
		return cmd.setupBrowserIDE(ide.Name, setupInfo, ide.Options)
	}

	return installNotebookIDE(setupInfo, ide)
}

func installNotebookIDE(
	setupInfo *config.Result,
	ide *provider2.WorkspaceIDEConfig,
) error {
	user := config.GetRemoteUser(setupInfo)
	folder := setupInfo.SubstitutionContext.ContainerWorkspaceFolder
	switch ide.Name {
	case string(config2.IDEFleet):
		return fleet.NewFleetServer(user, ide.Options).Install(folder)
	case string(config2.IDEJupyterNotebook):
		return jupyter.NewJupyterNotebookServer(folder, user, ide.Options).Install()
	case string(config2.IDEMarimo):
		return marimo.NewMarimoServer(folder, user, ide.Options).Install()
	case string(config2.IDERStudio):
		return rstudio.NewRStudioServer(folder, user, ide.Options).Install()
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
	formattedSettings, err := formatVSCodeSettings(vsCodeConfiguration.Settings)
	if err != nil {
		return err
	}
	log.Debugf("vscode settings:\n%s", formattedSettings)
	settings, err := vscodeSettingsJSON(vsCodeConfiguration.Settings)
	if err != nil {
		return err
	}

	user := config.GetRemoteUser(setupInfo)
	err = vscode.NewVSCodeServer(vscode.ServerOptions{
		Extensions: vsCodeConfiguration.Extensions,
		Settings:   settings,
		UserName:   user,
		Values:     ideOptions,
		Flavor:     flavor,
	}).Install()
	if err != nil {
		return err
	}

	if len(vsCodeConfiguration.Extensions) == 0 {
		return nil
	}

	return cmd.startExtensionsInstallInBackground(
		fmt.Sprintf("%s-async", flavor),
		"vscode-async",
		vsCodeConfiguration.Extensions,
		names.Flag(names.Flavor), string(flavor),
	)
}

func formatVSCodeSettings(settings map[string]any) (string, error) {
	formatted, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func vscodeSettingsJSON(settings map[string]any) (string, error) {
	if len(settings) == 0 {
		return "", nil
	}
	out, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}

	return string(out), nil
}

// setupBrowserIDE installs and starts any of the browser-based IDEs
// (openvscode, code-server, VS Code Web). The per-IDE differences live in the
// browserIDEs registry, so the install/extensions/start flow is written once.
func (cmd *SetupContainerCmd) setupBrowserIDE(
	ideName string,
	setupInfo *config.Result,
	ideOptions map[string]config2.OptionValue,
) error {
	b, ok := browserIDEByName(ideName)
	if !ok {
		return fmt.Errorf("unknown browser IDE %q", ideName)
	}
	log.Debugf("setup %s", b.name)

	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	settings, err := vscodeSettingsJSON(vsCodeConfiguration.Settings)
	if err != nil {
		return err
	}

	server := b.newServer(browserServerSpec{
		extensions: vsCodeConfiguration.Extensions,
		settings:   settings,
		userName:   config.GetRemoteUser(setupInfo),
		host:       "0.0.0.0",
		port:       strconv.Itoa(b.defaultPort),
		values:     ideOptions,
	})

	if err := server.Install(); err != nil {
		return err
	}

	if len(vsCodeConfiguration.Extensions) > 0 {
		if err := cmd.startBrowserExtensionsInstall(b, vsCodeConfiguration.Extensions); err != nil {
			return fmt.Errorf("install extensions: %w", err)
		}
	}

	return server.Start()
}

// startBrowserExtensionsInstall launches the IDE's `<ide>-async` command as a
// background process so a slow marketplace fetch never blocks setup.
func (cmd *SetupContainerCmd) startBrowserExtensionsInstall(
	b browserIDE,
	extensions []string,
) error {
	return cmd.startExtensionsInstallInBackground(b.asyncCmd, b.asyncCmd, extensions)
}

func (cmd *SetupContainerCmd) startExtensionsInstallInBackground(
	lockKey, subcommand string,
	extensions []string,
	extraArgs ...string,
) error {
	return command.StartBackgroundOnce(lockKey, func() (*exec.Cmd, error) {
		log.Infof(
			"installing extensions in the background: %s",
			strings.Join(extensions, ","),
		)
		binaryPath, err := os.Executable()
		if err != nil {
			return nil, err
		}

		args := []string{
			cmdInternal, cmdAgent, cmdContainer, subcommand,
			names.Flag(names.SetupInfo), cmd.SetupInfo,
		}
		args = append(args, extraArgs...)
		//nolint:gosec // binaryPath is from os.Executable(), not user input
		return exec.Command(binaryPath, args...), nil
	})
}
