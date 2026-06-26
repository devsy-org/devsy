package agentcontainer

import (
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide/codeserver"
	"github.com/devsy-org/devsy/pkg/ide/openvscode"
	"github.com/devsy-org/devsy/pkg/ide/vscodeweb"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// browserServer is the shared behavior of the in-container browser IDEs
// (openvscode, code-server, VS Code Web). Each pkg/ide implementation already
// satisfies this, so the setup and async-extension flows can be written once.
type browserServer interface {
	Install() error
	InstallExtensions() error
	Start() error
}

// browserServerSpec holds the inputs a browser IDE needs to be constructed.
// The extension-install path (browserAsyncCmd) leaves settings/host/port unset;
// the full setup path fills them in.
type browserServerSpec struct {
	extensions []string
	settings   string
	userName   string
	host       string
	port       string
	values     map[string]config2.OptionValue
}

// browserIDE describes a single browser-based IDE. The per-IDE differences are
// data (names, default port, constructor) rather than duplicated control flow.
type browserIDE struct {
	name        config2.IDE
	asyncCmd    string
	short       string
	defaultPort int
	newServer   func(browserServerSpec) browserServer
}

// browserIDEs is the registry driving both async-extension commands and the
// setupBrowserIDE flow. Adding a browser IDE is one entry plus its pkg/ide
// implementation.
var browserIDEs = []browserIDE{
	{
		name:        config2.IDEOpenVSCode,
		asyncCmd:    "openvscode-async",
		short:       "Starts openvscode",
		defaultPort: openvscode.DefaultVSCodePort,
		newServer: func(s browserServerSpec) browserServer {
			return openvscode.NewOpenVSCodeServer(
				s.extensions, s.settings, s.userName, s.host, s.port, s.values,
			)
		},
	},
	{
		name:        config2.IDECodeServer,
		asyncCmd:    "code-server-async",
		short:       "Starts code-server",
		defaultPort: codeserver.DefaultCodeServerPort,
		newServer: func(s browserServerSpec) browserServer {
			return codeserver.NewCodeServer(codeserver.ServerOptions{
				Extensions: s.extensions,
				Settings:   s.settings,
				UserName:   s.userName,
				Host:       s.host,
				Port:       s.port,
				Values:     s.values,
			})
		},
	},
	{
		name:        config2.IDEVSCodeWeb,
		asyncCmd:    "vscode-web-async",
		short:       "Starts VS Code Web",
		defaultPort: vscodeweb.DefaultVSCodeWebPort,
		newServer: func(s browserServerSpec) browserServer {
			return vscodeweb.NewVSCodeWeb(vscodeweb.ServerOptions{
				Extensions: s.extensions,
				Settings:   s.settings,
				UserName:   s.userName,
				Host:       s.host,
				Port:       s.port,
				Values:     s.values,
			})
		},
	},
}

func browserIDEByName(name string) (browserIDE, bool) {
	for _, b := range browserIDEs {
		if string(b.name) == name {
			return b, true
		}
	}
	return browserIDE{}, false
}

// browserAsyncCmd holds the cmd flags for a browser IDE's background
// extension-install command.
type browserAsyncCmd struct {
	*flags.GlobalFlags

	ide       browserIDE
	SetupInfo string
}

// newBrowserAsyncCmd builds the `<ide>-async` command that installs the IDE's
// extensions in the background, decoupled from the foreground setup so a slow
// marketplace fetch never blocks the workspace coming up.
func newBrowserAsyncCmd(b browserIDE) *cobra.Command {
	cmd := &browserAsyncCmd{ide: b}
	cobraCmd := &cobra.Command{
		Use:   b.asyncCmd,
		Short: b.short,
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	cobraCmd.Flags().StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	_ = cobraCmd.MarkFlagRequired("setup-info")
	return cobraCmd
}

// Run runs the command logic.
func (cmd *browserAsyncCmd) Run(_ *cobra.Command, _ []string) error {
	log.Debugf("Start setting up container")
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return err
	}

	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	server := cmd.ide.newServer(browserServerSpec{
		extensions: vsCodeConfiguration.Extensions,
		userName:   config.GetRemoteUser(setupInfo),
	})
	return server.InstallExtensions()
}
