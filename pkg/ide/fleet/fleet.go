package fleet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	copy2 "github.com/devsy-org/devsy/pkg/copy"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/devsy-org/devsy/pkg/util"
)

const (
	FleetURLFileName    = config.BinaryName + "-fleet.url.txt"
	VersionOption       = "VERSION"
	DownloadAmd64Option = "DOWNLOAD_AMD64"
	DownloadArm64Option = "DOWNLOAD_ARM64"
)

var fleetVersionRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

var Options = ide.Options{
	VersionOption: {
		Name:        VersionOption,
		Description: "The version of fleet to install",
		Default:     "latest",
	},
	DownloadArm64Option: {
		Name:        DownloadArm64Option,
		Description: "The download url for the arm64 install script",
		Default:     "https://download.jetbrains.com/product?code=FLL&release.type=preview&release.type=eap&platform=linux_aarch64",
	},
	DownloadAmd64Option: {
		Name:        DownloadAmd64Option,
		Description: "The download url for the amd64 install script",
		Default:     "https://download.jetbrains.com/product?code=FLL&release.type=preview&release.type=eap&platform=linux_x64",
	},
}

func NewFleetServer(
	userName string,
	values map[string]config.OptionValue,
) *FleetServer {
	return &FleetServer{
		values:   values,
		userName: userName,
	}
}

type FleetServer struct {
	values   map[string]config.OptionValue
	userName string
}

func (o *FleetServer) Install(projectDir string) error {
	location, err := prepareFleetServerLocation(o.userName)
	if err != nil {
		return err
	}

	// is installed
	fleetBinary := filepath.Join(location, "fleet")
	_, err = os.Stat(fleetBinary)
	if err == nil {
		return o.Start(fleetBinary, location, projectDir)
	}

	// check what release we need to download
	var url string
	if runtime.GOARCH == "arm64" {
		url = Options.GetValue(o.values, DownloadArm64Option)
	} else {
		url = Options.GetValue(o.values, DownloadAmd64Option)
	}

	// download binary
	log.Infof("Downloading fleet")
	if err := devsyhttp.DownloadToFile(
		context.Background(), url, fleetBinary, devsyhttp.WithMode(0o755),
	); err != nil {
		return err
	}

	// chown location
	if o.userName != "" {
		err = copy2.ChownR(location, o.userName)
		if err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}

	log.Infof("downloaded fleet")
	return o.Start(fleetBinary, location, projectDir)
}

func (o *FleetServer) Start(binaryPath, location, projectDir string) error {
	wasStarted := false
	var readCloser io.ReadCloser
	stderrBuffer := &bytes.Buffer{}

	err := command.StartBackgroundOnce("fleet", func() (*exec.Cmd, error) {
		log.Infof("Starting fleet in background")
		version := Options.GetValue(o.values, VersionOption)
		runCommand, err := fleetRunCommand(binaryPath, projectDir, location, version)
		if err != nil {
			return nil, err
		}
		cmd := o.commandForShell(runCommand)
		cmd.Dir = location
		readCloser, err = cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		cmd.Stderr = stderrBuffer
		wasStarted = true
		return cmd, nil
	})
	if err != nil {
		return err
	} else if !wasStarted {
		return nil
	}
	defer func() { _ = readCloser.Close() }()

	// wait for the jet brains url and then exit
	log.Infof("waiting for fleet to start")
	return o.waitForFleetURL(readCloser, stderrBuffer, location)
}

func fleetRunCommand(binaryPath, projectDir, location, version string) (string, error) {
	if version == "latest" {
		return fmt.Sprintf(
			"%s launch workspace -- --projectDir %q --cache-path %q --auth=accept-everyone --publish --enableSmartMode",
			binaryPath,
			projectDir,
			location,
		), nil
	}
	if !fleetVersionRegex.MatchString(version) {
		return "", fmt.Errorf(
			"invalid fleet version %q: must match %s",
			version,
			fleetVersionRegex.String(),
		)
	}
	return fmt.Sprintf(
		"%s launch workspace --workspace-version %q -- --projectDir %q --cache-path %q "+
			"--auth=accept-everyone --publish --enableSmartMode",
		binaryPath,
		version,
		projectDir,
		location,
	), nil
}

func (o *FleetServer) commandForShell(runCommand string) *exec.Cmd {
	var args []string
	if o.userName != "" {
		args = []string{"su", o.userName, "-c", runCommand}
	} else {
		args = []string{"sh", "-c", runCommand}
	}
	// #nosec G204 -- runCommand is assembled from a fixed template with validated/quoted
	// arguments (version is regex-validated, paths are %q-quoted), so it cannot be exploited.
	return exec.Command(args[0], args[1:]...)
}

func (o *FleetServer) waitForFleetURL(
	readCloser io.ReadCloser,
	stderrBuffer *bytes.Buffer,
	location string,
) error {
	s := scanner.NewScanner(readCloser)
	stdoutBuffer := &bytes.Buffer{}
	for s.Scan() {
		text := s.Text()
		if !strings.Contains(text, "https://fleet.jetbrains.com/") {
			_, _ = stdoutBuffer.Write([]byte(text + "\n"))
			continue
		}

		index := strings.Index(text, "https://fleet.jetbrains.com/")
		fleetURLFile := filepath.Join(location, FleetURLFileName)
		err := os.WriteFile(
			fleetURLFile,
			[]byte(strings.TrimSpace(text[index:])),
			0o600,
		) // #nosec G703
		if err != nil {
			return err
		}

		log.Infof("fleet started")
		return o.startMonitor()
	}

	return fmt.Errorf(
		"seems like there was an error starting up fleet: %s%s",
		stdoutBuffer.String(),
		stderrBuffer.String(),
	)
}

func (o *FleetServer) startMonitor() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}

	return command.StartBackgroundOnce("fleet-monitor", func() (*exec.Cmd, error) {
		log.Infof("starting fleet monitor in background")
		runCommand := fmt.Sprintf("%s internal fleet-server --workspaceid %s", self, "test")
		return o.commandForShell(runCommand), nil
	})
}

func prepareFleetServerLocation(userName string) (string, error) {
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

	folder := filepath.Join(homeFolder, ".fleet-server")
	err = os.MkdirAll(folder, 0o755) // #nosec G301
	if err != nil {
		return "", err
	}

	return folder, nil
}
