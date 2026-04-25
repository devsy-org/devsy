package tunnel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/gitsshsigning"
	"github.com/devsy-org/devsy/pkg/ide/openvscode"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/netstat"
	"github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/docker/go-connections/nat"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

const (
	debugFlag          = " --debug"
	defaultExitTimeout = 5 * time.Second
	maxRetrySteps      = 10
	retryDuration      = 1 * time.Second
	retryFactor        = 2.0
	retryJitter        = 0.1
)

// RunServicesOptions contains all options for running services.
type RunServicesOptions struct {
	DevsyConfig                    *config.Config
	ContainerClient                *ssh.Client
	User                           string
	ForwardPorts                   bool
	ExtraPorts                     []string
	PlatformOptions                *devsy.PlatformOptions
	Workspace                      *provider.Workspace
	ConfigureDockerCredentials     bool
	ConfigureGitCredentials        bool
	ConfigureGitSSHSignatureHelper bool
	GitSSHSigningKey               string
}

// getExitAfterTimeout calculates the timeout value based on configuration.
func getExitAfterTimeout(devsyConfig *config.Config) time.Duration {
	if devsyConfig.ContextOption(config.ContextOptionExitAfterTimeout) != config.BoolTrue {
		return 0
	}
	return defaultExitTimeout
}

// createForwarder creates a port forwarder if port forwarding is enabled.
func createForwarder(opts RunServicesOptions, forwardedPorts []string) netstat.Forwarder {
	if !opts.ForwardPorts {
		return nil
	}
	ports := append([]string{}, forwardedPorts...)
	ports = append(ports, fmt.Sprintf("%d", openvscode.DefaultVSCodePort))
	return newForwarder(opts.ContainerClient, ports)
}

// tunnelServerParams contains parameters for running the tunnel server.
type tunnelServerParams struct {
	opts         RunServicesOptions
	stdoutReader *os.File
	stdinWriter  *os.File
	forwarder    netstat.Forwarder
	errChan      chan error
}

// runTunnelServer runs the tunnel server in a goroutine.
func runTunnelServer(ctx context.Context, cancel context.CancelFunc, p tunnelServerParams) {
	defer cancel()
	defer func() { _ = p.stdoutReader.Close() }()
	defer func() { _ = p.stdinWriter.Close() }()
	err := tunnelserver.RunServicesServer(
		ctx,
		p.stdoutReader,
		p.stdinWriter,
		p.opts.ConfigureGitCredentials,
		p.opts.ConfigureDockerCredentials,
		p.forwarder,
		p.opts.Workspace,
		tunnelserver.WithPlatformOptions(p.opts.PlatformOptions),
	)
	if err != nil {
		p.errChan <- fmt.Errorf("run tunnel server: %w", err)
	}
	close(p.errChan)
}

// addGitSSHSigningKey adds SSH signing key to command if configured.
// When explicitKey is set (from --git-ssh-signing-key flag), it takes
// precedence over the host's .gitconfig. This ensures signing works
// even when user.signingkey is not in the host git configuration.
func addGitSSHSigningKey(command string, explicitKey string) string {
	userSigningKey := explicitKey
	if userSigningKey == "" {
		format, extracted, err := gitsshsigning.ExtractGitConfiguration()
		if err != nil {
			log.Debugf("failed to extract git configuration: %v", err)
			return command
		}
		if format != gitsshsigning.GPGFormatSSH || extracted == "" {
			return command
		}
		userSigningKey = extracted
	}
	encodedKey := base64.StdEncoding.EncodeToString([]byte(userSigningKey))
	command += fmt.Sprintf(" --git-user-signing-key %s", encodedKey)
	return command
}

// buildCredentialsCommand builds the credentials server command.
func buildCredentialsCommand(opts RunServicesOptions) string {
	command := fmt.Sprintf(
		"%s agent container credentials-server --user %s",
		shellescape.Quote(agent.ContainerDevsyHelperLocation),
		shellescape.Quote(opts.User),
	)
	if opts.ConfigureGitCredentials {
		command += " --configure-git-helper"
	}
	if opts.ConfigureGitSSHSignatureHelper {
		command = addGitSSHSigningKey(command, opts.GitSSHSigningKey)
	}
	if opts.ConfigureDockerCredentials {
		command += " --configure-docker-helper"
	}
	if opts.ForwardPorts {
		command += " --forward-ports"
	}
	if log.DebugEnabled() {
		command += debugFlag
	}
	return command
}

// runServicesIteration performs one iteration of the retry loop.
func runServicesIteration(
	ctx context.Context,
	opts RunServicesOptions,
	forwardedPorts []string,
) error {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdoutWriter.Close() }()

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		return err
	}
	defer func() { _ = stdinReader.Close() }()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	forwarder := createForwarder(opts, forwardedPorts)

	errChan := make(chan error, 1)
	go runTunnelServer(cancelCtx, cancel, tunnelServerParams{
		opts:         opts,
		stdoutReader: stdoutReader,
		stdinWriter:  stdinWriter,
		forwarder:    forwarder,
		errChan:      errChan,
	})

	writer := log.Writer(log.LevelDebug)
	defer func() { _ = writer.Close() }()

	command := buildCredentialsCommand(opts)

	err = devssh.Run(cancelCtx, devssh.RunOptions{
		Client:  opts.ContainerClient,
		Command: command,
		Stdin:   stdinReader,
		Stdout:  stdoutWriter,
		Stderr:  writer,
	})
	if err != nil {
		// Drain errChan to allow goroutine to exit cleanly
		select {
		case <-errChan:
		default:
		}
		return err
	}
	return <-errChan
}

// RunServices forwards the ports for a given workspace and uses its SSH client
// to run the credentials server remotely and the services server locally to
// communicate with the container.
func RunServices(ctx context.Context, opts RunServicesOptions) error {
	exitAfterTimeout := getExitAfterTimeout(opts.DevsyConfig)

	forwardedPorts, err := forwardDevContainerPorts(ctx, portForwardParams{
		containerClient:  opts.ContainerClient,
		extraPorts:       opts.ExtraPorts,
		exitAfterTimeout: exitAfterTimeout,
	})
	if err != nil {
		return fmt.Errorf("forward ports: %w", err)
	}

	return retry.OnError(wait.Backoff{
		Steps:    maxRetrySteps,
		Duration: retryDuration,
		Factor:   retryFactor,
		Jitter:   retryJitter,
	}, func(err error) bool {
		// Do not retry on context cancellation or deadline exceeded
		if ctx.Err() != nil {
			return false
		}
		return true
	}, func() error {
		return runServicesIteration(ctx, opts, forwardedPorts)
	})
}

// portForwardParams contains parameters for port forwarding.
type portForwardParams struct {
	containerClient  *ssh.Client
	extraPorts       []string
	exitAfterTimeout time.Duration
}

// forwardDevContainerPorts forwards all the ports defined in the devcontainer.json.
func forwardDevContainerPorts(ctx context.Context, p portForwardParams) ([]string, error) {
	result, err := getContainerResult(ctx, p)
	if err != nil {
		return nil, err
	}

	forwardedPorts := []string{}
	forwardedPorts = append(forwardedPorts, forwardExtraPorts(ctx, p)...)
	forwardedPorts = append(forwardedPorts, forwardAppPorts(ctx, p, result)...)
	forwardedPorts = append(forwardedPorts, forwardConfigPorts(ctx, p, result)...)

	return forwardedPorts, nil
}

// getContainerResult retrieves and parses the container result.
func getContainerResult(ctx context.Context, p portForwardParams) (*config2.Result, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := devssh.Run(ctx, devssh.RunOptions{
		Client:  p.containerClient,
		Command: "cat " + config.DevContainerResultPath,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"retrieve container result: %s\n%s: %w",
			stdout.String(),
			stderr.String(),
			err,
		)
	}

	result := &config2.Result{}
	err = json.Unmarshal(stdout.Bytes(), result)
	if err != nil {
		return nil, fmt.Errorf("error parsing container result %s: %w", stdout.String(), err)
	}
	log.Debugf("parsed container result from %s", config.DevContainerResultPath)

	return result, nil
}

// forwardExtraPorts forwards extra ports specified by the user.
func forwardExtraPorts(ctx context.Context, p portForwardParams) []string {
	forwardedPorts := []string{}
	for _, port := range p.extraPorts {
		forwardedPorts = append(forwardedPorts, forwardPort(ctx, singlePortForwardParams{
			containerClient:  p.containerClient,
			port:             port,
			exitAfterTimeout: p.exitAfterTimeout,
		})...)
	}
	return forwardedPorts
}

// forwardAppPorts forwards application ports from the devcontainer config.
func forwardAppPorts(ctx context.Context, p portForwardParams, result *config2.Result) []string {
	forwardedPorts := []string{}
	if result == nil || result.MergedConfig == nil {
		return forwardedPorts
	}
	for _, port := range result.MergedConfig.AppPort {
		forwardedPorts = append(forwardedPorts, forwardPort(ctx, singlePortForwardParams{
			containerClient:  p.containerClient,
			port:             port,
			exitAfterTimeout: 0,
		})...)
	}
	return forwardedPorts
}

// forwardConfigPorts forwards ports from the forwardPorts configuration.
func forwardConfigPorts(ctx context.Context, p portForwardParams, result *config2.Result) []string {
	forwardedPorts := []string{}
	if result == nil || result.MergedConfig == nil {
		return forwardedPorts
	}
	for _, port := range result.MergedConfig.ForwardPorts {
		host, portNumber, err := parseForwardPort(port)
		if err != nil {
			log.Debugf("error parsing forwardPort %s: %v", port, err)
			continue
		}

		// Forward port asynchronously to avoid blocking
		go func(port string) {
			log.Debugf("forward port %s", port)
			if err := devssh.PortForward(
				ctx,
				p.containerClient,
				"tcp",
				fmt.Sprintf("localhost:%d", portNumber),
				"tcp",
				fmt.Sprintf("%s:%d", host, portNumber),
				0,
			); err != nil {
				log.Errorf("error port forwarding %s: %v", port, err)
			}
		}(port)

		forwardedPorts = append(forwardedPorts, port)
	}
	return forwardedPorts
}

// singlePortForwardParams contains parameters for forwarding a single port.
type singlePortForwardParams struct {
	containerClient  *ssh.Client
	port             string
	exitAfterTimeout time.Duration
}

// forwardPort forwards a single port specification.
func forwardPort(ctx context.Context, p singlePortForwardParams) []string {
	parsed, err := nat.ParsePortSpec(p.port)
	if err != nil {
		log.Debugf("error parsing appPort %s: %v", p.port, err)
		return nil
	}

	// try to forward
	forwardedPorts := []string{}
	for _, parsedPort := range parsed {
		if parsedPort.Binding.HostIP == "" {
			parsedPort.Binding.HostIP = "localhost"
		}
		if parsedPort.Binding.HostPort == "" {
			parsedPort.Binding.HostPort = parsedPort.Port.Port()
		}
		go func(parsedPort nat.PortMapping) {
			// do the forward
			hostAddr := parsedPort.Binding.HostIP + ":" + parsedPort.Binding.HostPort
			containerAddr := "localhost:" + parsedPort.Port.Port()
			log.Debugf("forward port %s to %s", hostAddr, containerAddr)
			if err := devssh.PortForward(
				ctx,
				p.containerClient,
				"tcp",
				hostAddr,
				"tcp",
				containerAddr,
				p.exitAfterTimeout,
			); err != nil {
				log.Errorf(
					"error port forwarding %s:%s to %s: %v",
					parsedPort.Binding.HostIP,
					parsedPort.Binding.HostPort,
					parsedPort.Port.Port(),
					err,
				)
			}
		}(parsedPort)

		forwardedPorts = append(forwardedPorts, parsedPort.Binding.HostPort)
	}

	return forwardedPorts
}

// parseForwardPort parses a port specification into host and port number.
func parseForwardPort(port string) (string, int64, error) {
	tokens := strings.Split(port, ":")

	if len(tokens) == 1 {
		portNum, err := strconv.ParseInt(tokens[0], 10, 64)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port number %q: %w", port, err)
		}
		return "localhost", portNum, nil
	}

	if len(tokens) == 2 {
		portNum, err := strconv.ParseInt(tokens[1], 10, 64)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port number in %q: %w", port, err)
		}
		return tokens[0], portNum, nil
	}

	return "", 0, fmt.Errorf(
		"invalid forwardPorts port format %q (expected 'port' or 'host:port')",
		port,
	)
}
