package pro

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/machineid"
	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/devsy-org/devsy/pkg/util"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	ErrMissingContainer = errors.New("missing container")
	//nolint:staticcheck // ST1005: "Devsy Pro" is a proper noun
	ErrPlatformNotReachable = errors.New("Devsy Pro is not reachable")
)

type ContainerDetails struct {
	NetworkSettings ContainerNetworkSettings `json:"NetworkSettings"`
	State           ContainerDetailsState    `json:"State"`
	ID              string                   `json:"ID,omitempty"`
	Created         string                   `json:"Created,omitempty"`
	Config          ContainerDetailsConfig   `json:"Config"`
}

type ContainerNetworkSettings struct {
	Ports map[string][]ContainerPort `json:"ports,omitempty"`
}

type ContainerPort struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort,omitempty"`
}

type ContainerDetailsConfig struct {
	Labels map[string]string `json:"Labels,omitempty"`
	Image  string            `json:"Image,omitempty"`
	User   string            `json:"User,omitempty"`
	Env    []string          `json:"Env,omitempty"`
}

type ContainerDetailsState struct {
	Status    devcconfig.ContainerStatus `json:"Status,omitempty"`
	StartedAt string                     `json:"StartedAt,omitempty"`
}

func (cmd *StartCmd) startDocker(ctx context.Context) error {
	log.Infof("Starting Devsy Pro in Docker")
	name := config.ProReleaseName

	// prepare installation
	err := cmd.prepareDocker()
	if err != nil {
		return err
	}

	// try to find loft container
	containerID, err := cmd.findLoftContainer(ctx, name, true)
	if err != nil {
		return err
	}

	// check if container is there
	containerID, err = cmd.resetExistingContainer(ctx, containerID)
	if err != nil {
		return err
	}

	// Use default password if none is set
	if cmd.Password == "" {
		cmd.Password = getMachineUID()
	}

	// check if is installed
	if containerID != "" {
		log.Info("Existing instance found. Run with --upgrade to apply new configuration")
		return cmd.successDocker(ctx, containerID)
	}

	// Install Devsy
	log.Info("Welcome to Devsy Pro!")
	log.Info("This installer will help you get started.")

	// make sure we are ready for installing
	containerID, err = cmd.runInDocker(ctx, name)
	if err != nil {
		return err
	} else if containerID == "" {
		return fmt.Errorf(
			"%w: %s",
			ErrMissingContainer,
			"couldn't find Devsy container after starting it",
		)
	}

	return cmd.successDocker(ctx, containerID)
}

func (cmd *StartCmd) resetExistingContainer(
	ctx context.Context,
	containerID string,
) (string, error) {
	if containerID != "" && (cmd.Reset || cmd.Upgrade) {
		log.Info("Existing instance found.")
		if err := cmd.uninstallDocker(ctx, containerID); err != nil {
			return "", err
		}

		return "", nil
	}

	return containerID, nil
}

func (cmd *StartCmd) successDocker(ctx context.Context, containerID string) error {
	if cmd.NoWait {
		return nil
	}

	// wait until Devsy is ready
	host, err := cmd.waitForLoftDocker(ctx, containerID)
	if err != nil {
		return err
	}

	// wait for domain to become reachable
	log.Infof("Wait for Devsy Pro to become available at %s", host)
	err = wait.PollUntilContextTimeout(
		ctx,
		time.Second,
		time.Minute*10,
		true,
		func(ctx context.Context) (bool, error) {
			containerDetails, err := cmd.inspectContainer(ctx, containerID)
			if err != nil {
				return false, fmt.Errorf("inspect loft container: %w", err)
			} else if containerDetails.State.Status == devcconfig.ContainerStatusExited ||
				containerDetails.State.Status == devcconfig.ContainerStatusDead {
				logs, _ := cmd.logsContainer(ctx, containerID)
				return false, fmt.Errorf(
					"container failed (status: %s):\n %s",
					containerDetails.State.Status,
					logs,
				)
			}

			return isHostReachable(ctx, host)
		},
	)
	if err != nil {
		return fmt.Errorf("error waiting for Devsy Pro: %w", err)
	}

	// print success message
	PrintSuccessMessageDockerInstall(host, cmd.Password)
	return nil
}

func PrintSuccessMessageDockerInstall(host, password string) {
	url := "https://" + host
	fmt.Fprintf(os.Stderr, `

##########################   LOGIN   ############################

Username: `+greenBold("admin")+`
Password: `+greenBold(password)+`

Login via UI:  %s
Login via CLI: %s

#################################################################

Devsy Pro was successfully installed and can now be reached at: %s

Thanks for using Devsy Pro!
`,
		greenBold(url),
		greenBold("devsy pro login"+" "+url),
		url,
	)
}

func (cmd *StartCmd) waitForLoftDocker(ctx context.Context, containerID string) (string, error) {
	log.Info("Wait for Devsy Pro to become available")

	// check for local port
	containerDetails, err := cmd.inspectContainer(ctx, containerID)
	if err != nil {
		return "", err
	} else if len(containerDetails.NetworkSettings.Ports) > 0 &&
		len(containerDetails.NetworkSettings.Ports["10443/tcp"]) > 0 {
		return "localhost:" + containerDetails.NetworkSettings.Ports["10443/tcp"][0].HostPort, nil
	}

	// check if no tunnel
	if cmd.NoTunnel {
		return "", fmt.Errorf(
			"%w: %s",
			ErrPlatformNotReachable,
			"cannot connect to Devsy Pro as it has no exposed port and --no-tunnel is enabled",
		)
	}

	// wait for router
	url := ""
	waitErr := wait.PollUntilContextTimeout(
		ctx,
		time.Second,
		time.Minute*10,
		true,
		func(ctx context.Context) (bool, error) {
			url, err = cmd.findLoftRouter(ctx, containerID)
			if err != nil {
				return false, nil
			}

			return true, nil
		},
	)
	if waitErr != nil {
		return "", fmt.Errorf("error waiting for loft router domain: %w", err)
	}

	return url, nil
}

func (cmd *StartCmd) findLoftRouter(ctx context.Context, id string) (string, error) {
	out, err := cmd.buildDockerCmd(ctx, "exec", id, "cat", "/var/lib/loft/loft-domain.txt").Output()
	if err != nil {
		return "", WrapCommandError(out, err)
	}

	return strings.TrimSpace(string(out)), nil
}

func (cmd *StartCmd) prepareDocker() error {
	// test for helm and kubectl
	_, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf(
			"seems like docker is not installed. Docker is required for the installation of loft. " +
				"Visit https://docs.docker.com/engine/install/ for install instructions",
		)
	}

	output, err := exec.Command("docker", "ps").CombinedOutput()
	if err != nil {
		return fmt.Errorf("seems like there are issues with your docker cli: \n\n%s", output)
	}

	return nil
}

func (cmd *StartCmd) uninstallDocker(ctx context.Context, id string) error {
	log.Infof("Uninstalling")

	// stop container
	out, err := cmd.buildDockerCmd(ctx, "stop", id).Output()
	if err != nil {
		return fmt.Errorf("stop container: %w", WrapCommandError(out, err))
	}

	// remove container
	out, err = cmd.buildDockerCmd(ctx, "rm", id).Output()
	if err != nil {
		return fmt.Errorf("remove container: %w", WrapCommandError(out, err))
	}

	return nil
}

func (cmd *StartCmd) runInDocker(ctx context.Context, name string) (string, error) {
	args := []string{"run", "-d", "--name", name}
	if cmd.NoTunnel {
		args = append(args, "--env", "DISABLE_DEVSY_ROUTER=true")
	}
	if cmd.Password != "" {
		args = append(args, "--env", "ADMIN_PASSWORD_HASH="+hash.String(cmd.Password))
	}

	// run as root otherwise we get permission errors
	args = append(args, "-u", "root")

	// mount the loft lib
	args = append(args, "-v", "loft-data:/var/lib/loft")

	// set port
	if cmd.LocalPort != "" {
		args = append(args, "-p", cmd.LocalPort+":10443")
	}

	// set extra args
	args = append(args, cmd.DockerArgs...)

	// set image
	switch {
	case cmd.DockerImage != "":
		args = append(args, cmd.DockerImage)
	case cmd.Version != "":
		args = append(args, "ghcr.io/devsy-org/devsy-pro:"+strings.TrimPrefix(cmd.Version, "v"))
	default:
		args = append(args, "ghcr.io/devsy-org/devsy-pro:latest")
	}

	log.Infof("Start Devsy Pro via 'docker %s'", strings.Join(args, " "))
	runCmd := cmd.buildDockerCmd(ctx, args...)
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	err := runCmd.Run()
	if err != nil {
		return "", err
	}

	return cmd.findLoftContainer(ctx, name, false)
}

func (cmd *StartCmd) logsContainer(ctx context.Context, id string) (string, error) {
	args := []string{"logs", id}
	out, err := cmd.buildDockerCmd(ctx, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("logs container: %w", WrapCommandError(out, err))
	}

	return string(out), nil
}

func (cmd *StartCmd) inspectContainer(ctx context.Context, id string) (*ContainerDetails, error) {
	args := []string{"inspect", "--type", "container", id}
	out, err := cmd.buildDockerCmd(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", WrapCommandError(out, err))
	}

	containerDetails := []*ContainerDetails{}
	err = json.Unmarshal(out, &containerDetails)
	if err != nil {
		return nil, fmt.Errorf("parse inspect output: %w", err)
	} else if len(containerDetails) == 0 {
		return nil, fmt.Errorf("coudln't find container %s", id)
	}

	return containerDetails[0], nil
}

func (cmd *StartCmd) removeContainer(ctx context.Context, id string) error {
	args := []string{"rm", id}
	out, err := cmd.buildDockerCmd(ctx, args...).Output()
	if err != nil {
		return fmt.Errorf("remove container: %w", WrapCommandError(out, err))
	}

	return nil
}

func (cmd *StartCmd) findLoftContainer(
	ctx context.Context,
	name string,
	onlyRunning bool,
) (string, error) {
	args := []string{"ps", "-q", "-a", "-f", "name=^" + name + "$"}
	out, err := cmd.buildDockerCmd(ctx, args...).Output()
	if err != nil {
		// fallback to manual search
		return "", fmt.Errorf("error finding container: %w", WrapCommandError(out, err))
	}

	arr := []string{}
	scan := scanner.NewScanner(bytes.NewReader(out))
	for scan.Scan() {
		arr = append(arr, strings.TrimSpace(scan.Text()))
	}
	if len(arr) == 0 {
		return "", nil
	}

	return cmd.resolveRunningContainer(ctx, arr, onlyRunning)
}

func (cmd *StartCmd) resolveRunningContainer(
	ctx context.Context,
	containerIDs []string,
	onlyRunning bool,
) (string, error) {
	// remove the failed / exited containers
	runningContainerID := ""
	for _, containerID := range containerIDs {
		containerState, err := cmd.inspectContainer(ctx, containerID)
		switch {
		case err != nil:
			return "", err
		case onlyRunning && containerState.State.Status != devcconfig.ContainerStatusRunning:
			err = cmd.removeContainer(ctx, containerID)
			if err != nil {
				return "", err
			}
		default:
			runningContainerID = containerID
		}
	}

	return runningContainerID, nil
}

func (cmd *StartCmd) buildDockerCmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", args...) // #nosec G204
}

func WrapCommandError(stdout []byte, err error) error {
	if err == nil {
		return nil
	}

	return &Error{
		stdout: stdout,
		err:    err,
	}
}

type Error struct {
	err    error
	stdout []byte
}

func (e *Error) Error() string {
	message := ""
	if len(e.stdout) > 0 {
		message += string(e.stdout) + "\n"
	}

	var exitError *exec.ExitError
	if errors.As(e.err, &exitError) && len(exitError.Stderr) > 0 {
		message += string(exitError.Stderr) + "\n"
	}

	return message + e.err.Error()
}

func (e *Error) Unwrap() error {
	return e.err
}

func getMachineUID() string {
	id, err := machineid.ID()
	if err != nil {
		id = "error"
		log.Debugf("Error retrieving machine uid: %v", err)
	}
	// get $HOME to distinguish two users on the same machine
	// will be hashed later together with the ID
	home, err := util.UserHomeDir()
	if err != nil {
		home = "error"
		log.Debugf("Error retrieving machine home: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(id))
	mac.Write([]byte(home))
	return fmt.Sprintf("%x", mac.Sum(nil))
}
