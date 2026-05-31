package openvscode

import (
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
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/util"
)

const (
	DownloadAmd64Template = "https://github.com/gitpod-io/openvscode-server/releases/download/openvscode-server-%s/openvscode-server-%s-linux-x64.tar.gz"
	DownloadArm64Template = "https://github.com/gitpod-io/openvscode-server/releases/download/openvscode-server-%s/openvscode-server-%s-linux-arm64.tar.gz"
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

const DefaultVSCodePort = 10800

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

type OpenVSCodeServer struct {
	values     map[string]config.OptionValue
	extensions []string
	settings   string
	userName   string
	host       string
	port       string
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

	// download tar
	resp, err := devsyhttp.GetHTTPClient().Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	err = extract.Extract(resp.Body, location, extract.StripLevels(1))
	if err != nil {
		return fmt.Errorf("extract vscode: %w", err)
	}

	// chown location
	if o.userName != "" {
		err = copy2.ChownR(location, o.userName)
		if err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}

	// paste settings
	err = o.installSettings()
	if err != nil {
		return fmt.Errorf("install settings: %w", err)
	}

	return nil
}

func (o *OpenVSCodeServer) getReleaseUrl() string {
	var url string
	version := Options.GetValue(o.values, VersionOption)

	if runtime.GOARCH == "arm64" {
		url = Options.GetValue(o.values, DownloadArm64Option)
		if url == "" {
			url = fmt.Sprintf(DownloadArm64Template, version, version)
		}
	} else {
		url = Options.GetValue(o.values, DownloadAmd64Option)
		if url == "" {
			url = fmt.Sprintf(DownloadAmd64Template, version, version)
		}
	}

	return url
}

func (o *OpenVSCodeServer) installExtensions() error {
	if len(o.extensions) == 0 {
		return nil
	}

	location, err := prepareOpenVSCodeServerLocation(o.userName)
	if err != nil {
		return err
	}

	out := log.Writer(log.LevelInfo)
	defer func() { _ = out.Close() }()

	binaryPath := filepath.Join(location, "bin", "openvscode-server")
	for _, extension := range o.extensions {
		log.Info("Install extension " + extension + "")
		runCommand := fmt.Sprintf("%s --install-extension %q", binaryPath, extension)
		args := []string{}
		if o.userName != "" {
			args = append(args, "su", o.userName, "-c", runCommand)
		} else {
			args = append(args, "sh", "-c", runCommand)
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = out
		cmd.Stderr = out
		err = cmd.Run()
		if err != nil {
			log.Errorf("failed installing extension: extension=%s, error=%v", extension, err)
		} else {
			log.Infof("installed extension: extension=%s", extension)
		}
	}

	return nil
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
			"%s server-local --without-connection-token --host %q --port %q",
			binaryPath,
			o.host,
			o.port,
		)
		args := []string{}
		if o.userName != "" {
			args = append(args, "su", o.userName, "-c", runCommand)
		} else {
			args = append(args, "sh", "-c", runCommand)
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = location
		return cmd, nil
	})
}

func prepareOpenVSCodeServerLocation(userName string) (string, error) {
	var err error
	homeFolder := ""
	if userName != "" {
		homeFolder, err = command.GetHome(userName)
	} else {
		homeFolder, err = util.UserHomeDir()
	}
	if err != nil {
		return "", err
	}

	folder := filepath.Join(homeFolder, ".openvscode-server")
	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll(folder, 0o755)
	if err != nil {
		return "", err
	}

	return folder, nil
}
