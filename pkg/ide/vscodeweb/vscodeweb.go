package vscodeweb

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

// Release URL format for the standalone VS Code CLI. The CLI tarball contains a
// single `code` binary at its root; `code serve-web` fetches the actual server
// payload at runtime on first connect (requires outbound network access).
const (
	downloadAmd64Template = "https://update.code.visualstudio.com/%s/cli-linux-x64/stable"
	downloadArm64Template = "https://update.code.visualstudio.com/%s/cli-linux-arm64/stable"
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
		Description: "The address to bind VS Code Web to locally, e.g. 0.0.0.0:12345",
		Default:     "",
	},
	VersionOption: {
		Name: VersionOption,
		Description: "The VS Code version for the serve-web CLI " +
			"(a release version like 1.96.4, or 'latest')",
		Default: "1.96.4",
	},
	DownloadArm64Option: {
		Name:        DownloadArm64Option,
		Description: "The download url for the arm64 VS Code CLI",
	},
	DownloadAmd64Option: {
		Name:        DownloadAmd64Option,
		Description: "The download url for the amd64 VS Code CLI",
	},
}

const archArm64 = "arm64"

// DefaultVSCodeWebPort sits next to openvscode (10800) and code-server (10801)
// so a host running all three only collides under the FindAvailablePort
// fallback path, not by default.
const DefaultVSCodeWebPort = 10802

// ServerOptions configures a VSCodeWeb instance.
type ServerOptions struct {
	Extensions []string
	Settings   string
	UserName   string
	Host       string
	Port       string
	Values     map[string]config.OptionValue
}

// VSCodeWeb installs the official VS Code CLI and runs it as `code serve-web`
// inside the workspace, serving the browser IDE backed by the Microsoft
// Marketplace.
type VSCodeWeb struct {
	values     map[string]config.OptionValue
	extensions []string
	settings   string
	userName   string
	host       string
	port       string
}

func NewVSCodeWeb(opts ServerOptions) *VSCodeWeb {
	host := opts.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := opts.Port
	if port == "" {
		port = strconv.Itoa(DefaultVSCodeWebPort)
	}
	return &VSCodeWeb{
		values:     opts.Values,
		extensions: opts.Extensions,
		settings:   opts.Settings,
		userName:   opts.UserName,
		host:       host,
		port:       port,
	}
}

// Install downloads the VS Code CLI tarball, extracts the `code` binary under
// <home>/.vscode-web, and writes settings.json. Idempotent: returns nil without
// re-downloading when the binary is already present.
func (v *VSCodeWeb) Install() error {
	location, err := prepareVSCodeWebLocation(v.userName)
	if err != nil {
		return err
	}

	if _, err := os.Stat(binaryPath(location)); err == nil {
		return nil
	}

	vscode.InstallAPKRequirements()

	if err := downloadAndExtract(v.getReleaseURL(), location); err != nil {
		return err
	}

	if v.userName != "" {
		if err := copy2.ChownR(location, v.userName); err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}

	if err := v.installSettings(); err != nil {
		return fmt.Errorf("install settings: %w", err)
	}

	return nil
}

// InstallExtensions installs each requested extension via the VS Code CLI.
// Partial failures are logged and tolerated; an aggregated error is returned
// only when every requested extension fails to install.
func (v *VSCodeWeb) InstallExtensions() error {
	if err := v.installExtensions(); err != nil {
		return fmt.Errorf("install extensions: %w", err)
	}
	return nil
}

// Start launches `code serve-web` as a background process bound to host:port.
// Idempotent via command.StartBackgroundOnce. --accept-server-license-terms is
// required so the background process never blocks on a license prompt.
func (v *VSCodeWeb) Start() error {
	location, err := prepareVSCodeWebLocation(v.userName)
	if err != nil {
		return err
	}

	binary := binaryPath(location)
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("find binary: %w", err)
	}

	return command.StartBackgroundOnce("vscode-web", func() (*exec.Cmd, error) {
		log.Infof("Starting VS Code Web (serve-web) in background")
		// serve-web downloads the VS Code server payload on the first browser
		// connect, so the container needs outbound network access. Surfaced
		// here because the fetch happens inside the detached process and a
		// blocked connection would otherwise look like an unexplained hang.
		log.Infof(
			"VS Code Web fetches its server on first connect; the container needs " +
				"outbound access to update.code.visualstudio.com",
		)
		// --without-connection-token: the only published path is the devsy
		// port-forward tunnel, which is itself authenticated.
		runCommand := fmt.Sprintf(
			"%s serve-web --without-connection-token --accept-server-license-terms "+
				"--host %q --port %q --server-data-dir %q",
			binary, v.host, v.port, serverDataDir(location),
		)
		return suOrSh(v.userName, runCommand, location), nil
	})
}

func (v *VSCodeWeb) getReleaseURL() string {
	version := Options.GetValue(v.values, VersionOption)

	var url string
	if runtime.GOARCH == archArm64 {
		url = Options.GetValue(v.values, DownloadArm64Option)
		if url == "" {
			url = fmt.Sprintf(downloadArm64Template, version)
		}
	} else {
		url = Options.GetValue(v.values, DownloadAmd64Option)
		if url == "" {
			url = fmt.Sprintf(downloadAmd64Template, version)
		}
	}
	return url
}

func (v *VSCodeWeb) installExtensions() error {
	if len(v.extensions) == 0 {
		return nil
	}

	location, err := prepareVSCodeWebLocation(v.userName)
	if err != nil {
		return err
	}

	out := log.Writer(log.LevelInfo)
	defer func() { _ = out.Close() }()

	binary := binaryPath(location)
	var failed []string
	for _, extension := range v.extensions {
		log.Info("Install extension " + extension)
		runCommand := fmt.Sprintf(
			"%s serve-web --server-data-dir %q --install-extension %q",
			binary, serverDataDir(location), extension,
		)
		cmd := suOrSh(v.userName, runCommand, "")
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
	if len(failed) == len(v.extensions) {
		return fmt.Errorf("all %d extensions failed to install: %v", len(failed), failed)
	}
	return nil
}

// installSettings writes settings.json to serve-web's Machine config dir, which
// lives under <server-data-dir>/data/Machine.
func (v *VSCodeWeb) installSettings() error {
	if len(v.settings) == 0 {
		return nil
	}

	location, err := prepareVSCodeWebLocation(v.userName)
	if err != nil {
		return err
	}
	settingsDir := filepath.Join(serverDataDir(location), "data", "Machine")
	// #nosec G301 -- match openvscode-server convention for parity.
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(settingsDir, "settings.json"),
		[]byte(v.settings),
		0o600,
	); err != nil {
		return err
	}
	if v.userName != "" {
		if err := copy2.ChownR(location, v.userName); err != nil {
			return err
		}
	}
	return nil
}

// downloadAndExtract fetches the CLI tarball at url and extracts it under
// location. The VS Code CLI tarball holds a single `code` binary at its root,
// so no strip levels are applied. Cleans up a partial extraction on failure so
// retries start fresh.
func downloadAndExtract(url, location string) error {
	resp, err := devsyhttp.GetHTTPClient().Get(url) // #nosec G107 -- URL comes from VersionOption.
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("download VS Code CLI: %s returned %s", url, resp.Status)
	}

	if err := extract.Extract(resp.Body, location); err != nil {
		if rmErr := os.RemoveAll(location); rmErr != nil {
			log.Warnf("cleanup partial install: path=%s err=%v", location, rmErr)
		}
		return fmt.Errorf("extract VS Code CLI: %w", err)
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

// binaryPath is the extracted VS Code CLI binary path.
func binaryPath(location string) string {
	return filepath.Join(location, "code")
}

// serverDataDir is where serve-web stores its server payload and Machine
// settings, kept inside the install dir so it is cleaned up with it.
func serverDataDir(location string) string {
	return filepath.Join(location, "server-data")
}

// prepareVSCodeWebLocation returns the install dir for VS Code Web, creating it
// if needed. It is deliberately distinct from ~/.vscode-server (used by the
// desktop VS Code remote server, whose binary is itself named code-server).
func prepareVSCodeWebLocation(userName string) (string, error) {
	homeFolder, err := userHome(userName)
	if err != nil {
		return "", err
	}
	folder := filepath.Join(homeFolder, ".vscode-web")
	// #nosec G301 -- match openvscode-server convention for parity.
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	return folder, nil
}
