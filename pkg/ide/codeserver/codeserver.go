package codeserver

import (
	"fmt"
	"net/http"
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

// Release URL format: tag has the `v` prefix; the asset filename does not.
const (
	downloadAmd64Template = "https://github.com/coder/code-server/releases/download/" +
		"v%s/code-server-%s-linux-amd64.tar.gz"
	downloadArm64Template = "https://github.com/coder/code-server/releases/download/" +
		"v%s/code-server-%s-linux-arm64.tar.gz"
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
		Enum:        []string{"true", "false"},
	},
	BindAddressOption: {
		Name:        BindAddressOption,
		Description: "The address to bind code-server to locally, e.g. 0.0.0.0:12345",
		Default:     "",
	},
	VersionOption: {
		Name:        VersionOption,
		Description: "The version for the code-server binary (without the leading v)",
		Default:     "4.121.0",
	},
	DownloadArm64Option: {
		Name:        DownloadArm64Option,
		Description: "The download url for the arm64 code-server binary",
	},
	DownloadAmd64Option: {
		Name:        DownloadAmd64Option,
		Description: "The download url for the amd64 code-server binary",
	},
}

// DefaultCodeServerPort sits next to openvscode's 10800 so a host running both
// IDEs only collides under the FindAvailablePort fallback path, not by default.
const DefaultCodeServerPort = 10801

// ServerOptions configures a CodeServer instance.
type ServerOptions struct {
	Extensions []string
	Settings   string
	UserName   string
	Host       string
	Port       string
	Values     map[string]config.OptionValue
}

// CodeServer installs and runs code-server (coder.com) inside the workspace.
type CodeServer struct {
	values     map[string]config.OptionValue
	extensions []string
	settings   string
	userName   string
	host       string
	port       string
}

func NewCodeServer(opts ServerOptions) *CodeServer {
	host := opts.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := opts.Port
	if port == "" {
		port = strconv.Itoa(DefaultCodeServerPort)
	}
	return &CodeServer{
		values:     opts.Values,
		extensions: opts.Extensions,
		settings:   opts.Settings,
		userName:   opts.UserName,
		host:       host,
		port:       port,
	}
}

// Install downloads the code-server release tarball, extracts it under
// <home>/.code-server, and writes settings.json. Idempotent: returns nil
// without re-downloading when the binary is already present.
func (c *CodeServer) Install() error {
	location, err := prepareCodeServerLocation(c.userName)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(location, "bin", "code-server")); err == nil {
		return nil
	}

	vscode.InstallAPKRequirements()

	if err := downloadAndExtract(c.getReleaseURL(), location); err != nil {
		return err
	}

	if c.userName != "" {
		if err := copy2.ChownR(location, c.userName); err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}

	if err := c.installSettings(); err != nil {
		return fmt.Errorf("install settings: %w", err)
	}

	return nil
}

// InstallExtensions installs each requested extension via the code-server CLI.
// Partial failures are logged and tolerated; an aggregated error is returned
// only when every requested extension fails to install.
func (c *CodeServer) InstallExtensions() error {
	if err := c.installExtensions(); err != nil {
		return fmt.Errorf("install extensions: %w", err)
	}
	return nil
}

// Start launches code-server as a background process bound to host:port.
// Idempotent via command.StartBackgroundOnce.
func (c *CodeServer) Start() error {
	location, err := prepareCodeServerLocation(c.userName)
	if err != nil {
		return err
	}

	binaryPath := filepath.Join(location, "bin", "code-server")
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("find binary: %w", err)
	}

	homeFolder, err := userHome(c.userName)
	if err != nil {
		return err
	}
	// Pin --user-data-dir so the settings install path stays stable regardless
	// of XDG_DATA_HOME in the container environment.
	userDataDir := filepath.Join(homeFolder, ".local", "share", "code-server")

	return command.StartBackgroundOnce("code-server", func() (*exec.Cmd, error) {
		log.Infof("Starting code-server in background")
		// --auth none is safe for clients outside the container because the
		// only published path is the devsy port-forward tunnel, which is
		// itself authenticated. Intra-container access is still unauthenticated.
		runCommand := fmt.Sprintf(
			"%s --bind-addr %q --user-data-dir %q --auth none "+
				"--disable-telemetry --disable-update-check",
			binaryPath, c.host+":"+c.port, userDataDir,
		)
		return suOrSh(c.userName, runCommand, location), nil
	})
}

func (c *CodeServer) getReleaseURL() string {
	version := Options.GetValue(c.values, VersionOption)

	var url string
	if runtime.GOARCH == "arm64" {
		url = Options.GetValue(c.values, DownloadArm64Option)
		if url == "" {
			url = fmt.Sprintf(downloadArm64Template, version, version)
		}
	} else {
		url = Options.GetValue(c.values, DownloadAmd64Option)
		if url == "" {
			url = fmt.Sprintf(downloadAmd64Template, version, version)
		}
	}
	return url
}

func (c *CodeServer) installExtensions() error {
	if len(c.extensions) == 0 {
		return nil
	}

	location, err := prepareCodeServerLocation(c.userName)
	if err != nil {
		return err
	}

	out := log.Writer(log.LevelInfo)
	defer func() { _ = out.Close() }()

	binaryPath := filepath.Join(location, "bin", "code-server")
	var failed []string
	for _, extension := range c.extensions {
		log.Info("Install extension " + extension)
		runCommand := fmt.Sprintf("%s --install-extension %q", binaryPath, extension)
		cmd := suOrSh(c.userName, runCommand, "")
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			log.Errorf("failed installing extension: extension=%s, error=%v", extension, err)
			failed = append(failed, extension)
		} else {
			log.Infof("installed extension: extension=%s", extension)
		}
	}

	// Escalate only on total failure — partial failures stay as logged
	// warnings to match openvscode's behavior.
	if len(failed) == len(c.extensions) {
		return fmt.Errorf("all %d extensions failed to install: %v", len(failed), failed)
	}
	return nil
}

// installSettings writes settings.json to code-server's user config dir,
// scoping chown to the code-server subtree to avoid clobbering other ~/.local
// ownership.
func (c *CodeServer) installSettings() error {
	if len(c.settings) == 0 {
		return nil
	}

	homeFolder, err := userHome(c.userName)
	if err != nil {
		return err
	}
	codeServerDataDir := filepath.Join(homeFolder, ".local", "share", "code-server")
	settingsDir := filepath.Join(codeServerDataDir, "User")
	// #nosec G301 -- match openvscode-server convention for parity.
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(settingsDir, "settings.json"),
		[]byte(c.settings),
		0o600,
	); err != nil {
		return err
	}
	if c.userName != "" {
		if err := copy2.ChownR(codeServerDataDir, c.userName); err != nil {
			return err
		}
	}
	return nil
}

// downloadAndExtract fetches the release tarball at url and extracts it under
// location. Cleans up a partial extraction on failure so retries start fresh.
func downloadAndExtract(url, location string) error {
	resp, err := devsyhttp.GetHTTPClient().Get(url) // #nosec G107 -- URL comes from VersionOption.
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("download code-server: %s returned %s", url, resp.Status)
	}

	if err := extract.Extract(resp.Body, location, extract.StripLevels(1)); err != nil {
		if rmErr := os.RemoveAll(location); rmErr != nil {
			log.Warnf("cleanup partial install: path=%s err=%v", location, rmErr)
		}
		return fmt.Errorf("extract code-server: %w", err)
	}
	return nil
}

// suOrSh builds an *exec.Cmd that runs runCommand either as the unprivileged
// user (via `su <user> -c`) or as the current user (via `sh -c`). All arg
// elements are constants from this package — runCommand itself is built from
// constants plus internal values, not user-controlled binary paths.
func suOrSh(userName, runCommand, workingDir string) *exec.Cmd {
	var cmd *exec.Cmd
	if userName != "" {
		// #nosec G204 -- args are fixed constants; runCommand is internally built.
		cmd = exec.Command("su", userName, "-c", runCommand)
	} else {
		// #nosec G204 -- args are fixed constants; runCommand is internally built.
		cmd = exec.Command("sh", "-c", runCommand)
	}
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	return cmd
}

func userHome(userName string) (string, error) {
	if userName != "" {
		return command.GetHome(userName)
	}
	return util.UserHomeDir()
}

// prepareCodeServerLocation returns the install dir for code-server, creating
// it if needed. Layout mirrors the upstream tarball after StripLevels(1):
// <home>/.code-server/bin/code-server.
func prepareCodeServerLocation(userName string) (string, error) {
	homeFolder, err := userHome(userName)
	if err != nil {
		return "", err
	}
	folder := filepath.Join(homeFolder, ".code-server")
	// #nosec G301 -- match openvscode-server convention for parity.
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	return folder, nil
}
