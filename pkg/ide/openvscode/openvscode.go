package openvscode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	copy2 "github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/internal/serverutil"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	DownloadAmd64Template = "https://github.com/gitpod-io/openvscode-server/releases/download/" +
		"openvscode-server-%s/openvscode-server-%s-linux-x64.tar.gz"
	DownloadArm64Template = "https://github.com/gitpod-io/openvscode-server/releases/download/" +
		"openvscode-server-%s/openvscode-server-%s-linux-arm64.tar.gz"
)

const (
	ForwardPortsOption  = "FORWARD_PORTS"
	BindAddressOption   = "BIND_ADDRESS"
	VersionOption       = "VERSION"
	DownloadAmd64Option = "DOWNLOAD_AMD64"
	DownloadArm64Option = "DOWNLOAD_ARM64"
)

var Options = ide.Options{
	ForwardPortsOption: {
		Name:        ForwardPortsOption,
		Description: "If Devsy should automatically do port-forwarding",
		Default:     "true",
		Enum: []string{
			"true",
			"false",
		},
	},
	BindAddressOption: {
		Name:        BindAddressOption,
		Description: "The address to bind VSCode web to locally, e.g. 0.0.0.0:12345",
		Default:     "",
	},
	VersionOption: {
		Name:        VersionOption,
		Description: "The version for the open vscode binary",
		Default:     "v1.109.5",
	},
	DownloadArm64Option: {
		Name:        DownloadArm64Option,
		Description: "The download url for the arm64 vscode server binary",
	},
	DownloadAmd64Option: {
		Name:        DownloadAmd64Option,
		Description: "The download url for the amd64 vscode server binary",
	},
}

var downloadSpec = serverutil.ReleaseSpec{
	Options:       Options,
	Amd64Option:   DownloadAmd64Option,
	Arm64Option:   DownloadArm64Option,
	Amd64Template: DownloadAmd64Template,
	Arm64Template: DownloadArm64Template,
}

const DefaultVSCodePort = 10800

type OpenVSCodeServer struct {
	values     map[string]config.OptionValue
	extensions []string
	settings   string
	userName   string
	host       string
	port       string
}

func NewOpenVSCodeServer(
	extensions []string,
	settings string,
	userName string,
	host, port string,
	values map[string]config.OptionValue,
) *OpenVSCodeServer {
	return &OpenVSCodeServer{
		values:     values,
		extensions: extensions,
		settings:   settings,
		userName:   userName,
		host:       host,
		port:       port,
	}
}

func (o *OpenVSCodeServer) InstallExtensions() error {
	// install extensions
	err := o.installExtensions()
	if err != nil {
		return fmt.Errorf("install extensions: %w", err)
	}

	return nil
}

func (o *OpenVSCodeServer) Install() error {
	location, err := prepareOpenVSCodeServerLocation(o.userName)
	if err != nil {
		return err
	}

	// is installed
	_, err = os.Stat(filepath.Join(location, "bin"))
	if err == nil {
		return nil
	}

	// check what release we need to download
	url := o.getReleaseUrl()

	vscode.InstallAPKRequirements()

	if err := ide.DownloadAndExtract(
		context.Background(), url, location, extract.StripLevels(1),
	); err != nil {
		return err
	}

	if err := serverutil.ChownIfNeeded(o.userName, location); err != nil {
		return err
	}

	if err := o.installSettings(); err != nil {
		return fmt.Errorf("install settings: %w", err)
	}

	return nil
}

func (o *OpenVSCodeServer) Start() error {
	location, err := prepareOpenVSCodeServerLocation(o.userName)
	if err != nil {
		return err
	}

	if o.host == "" {
		o.host = "0.0.0.0"
	}
	if o.port == "" {
		o.port = strconv.Itoa(DefaultVSCodePort)
	}

	binaryPath := filepath.Join(location, "bin", "openvscode-server")
	_, err = os.Stat(binaryPath)
	if err != nil {
		return fmt.Errorf("find binary: %w", err)
	}

	return command.StartBackgroundOnce("openvscode", func() (*exec.Cmd, error) {
		log.Infof("Starting openvscode in background")
		runCommand := fmt.Sprintf(
			"%s server-local --without-connection-token --host '%s' --port '%s'",
			binaryPath,
			o.host,
			o.port,
		)
		return serverutil.SuOrSh(o.userName, runCommand, location), nil
	})
}

func (o *OpenVSCodeServer) getReleaseUrl() string {
	version := Options.GetValue(o.values, VersionOption)
	return downloadSpec.URL(o.values, runtime.GOARCH, version, version)
}

func (o *OpenVSCodeServer) installExtensions() error {
	if len(o.extensions) == 0 {
		return nil
	}

	location, err := prepareOpenVSCodeServerLocation(o.userName)
	if err != nil {
		return err
	}

	binaryPath := filepath.Join(location, "bin", "openvscode-server")
	return serverutil.ExtensionInstall{
		UserName:   o.userName,
		Extensions: o.extensions,
		Command: func(extension string) string {
			return fmt.Sprintf("%s --install-extension '%s'", binaryPath, extension)
		},
	}.Run()
}

func (o *OpenVSCodeServer) installSettings() error {
	if len(o.settings) == 0 {
		return nil
	}

	location, err := prepareOpenVSCodeServerLocation(o.userName)
	if err != nil {
		return err
	}

	settingsDir := filepath.Join(location, "data", "Machine")
	// #nosec G301,G306 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll(settingsDir, 0o755)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(o.settings), 0o600)
	if err != nil {
		return err
	}

	err = copy2.ChownR(location, o.userName)
	if err != nil {
		return err
	}

	return nil
}

func prepareOpenVSCodeServerLocation(userName string) (string, error) {
	return serverutil.PrepareLocation(userName, ".openvscode-server")
}
