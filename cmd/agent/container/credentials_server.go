package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/credentials"
	devconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/gitsshsigning"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/netstat"
	portpkg "github.com/devsy-org/devsy/pkg/port"
	"github.com/spf13/cobra"
)

const ExitCodeIO int = 64

// CredentialsServerCmd holds the cmd flags.
type CredentialsServerCmd struct {
	*flags.GlobalFlags

	User string

	ConfigureGitHelper    bool
	ConfigureDockerHelper bool

	ForwardPorts      bool
	GitUserSigningKey string
}

// NewCredentialsServerCmd creates a new command.
func NewCredentialsServerCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &CredentialsServerCmd{
		GlobalFlags: flags,
	}
	credentialsServerCmd := &cobra.Command{
		Use:   "credentials-server",
		Short: "Starts a credentials server",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			port, err := credentials.GetPort()
			if err != nil {
				return err
			}

			return cmd.Run(c.Context(), port)
		},
	}
	credentialsServerCmd.Flags().
		BoolVar(&cmd.ConfigureGitHelper, "configure-git-helper", false, "If true will configure git helper")
	credentialsServerCmd.Flags().
		BoolVar(&cmd.ConfigureDockerHelper, "configure-docker-helper", false, "If true will configure docker helper")
	credentialsServerCmd.Flags().
		BoolVar(&cmd.ForwardPorts, "forward-ports", false,
			"If true will automatically try to forward open ports within the container")
	credentialsServerCmd.Flags().StringVar(&cmd.GitUserSigningKey, "git-user-signing-key", "", "")
	credentialsServerCmd.Flags().StringVar(&cmd.User, "user", "", "The user to use")
	_ = credentialsServerCmd.MarkFlagRequired("user")

	return credentialsServerCmd
}

// Run runs the command logic.
func (cmd *CredentialsServerCmd) Run(ctx context.Context, port int) error {
	// create a grpc client
	tunnelClient, err := tunnelserver.NewTunnelClient(os.Stdin, os.Stdout, true, ExitCodeIO)
	if err != nil {
		return fmt.Errorf("error creating tunnel client: %w", err)
	}

	// this message serves as a ping to the client
	_, err = tunnelClient.Ping(ctx, &tunnel.Empty{})
	if err != nil {
		return fmt.Errorf("ping client: %w", err)
	}

	// forward ports
	if cmd.ForwardPorts {
		go func() {
			log.Debugf("Start watching & forwarding open ports")
			err = forwardPorts(ctx, tunnelClient)
			if err != nil {
				log.Errorf("error forwarding ports: %v", err)
			}
		}()
	}

	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	if ok, err := portpkg.IsAvailable(addr); !ok || err != nil {
		log.Debugf("Port %d not available, exiting", port)
		return nil
	}

	// configure docker credential helper
	if cmd.ConfigureDockerHelper {
		err = dockercredentials.ConfigureCredentialsContainer(cmd.User, port)
		if err != nil {
			return err
		}
	}

	// configure git user
	err = configureGitUserLocally(ctx, cmd.User, tunnelClient)
	if err != nil {
		log.Debugf("Error configuring git user: %v", err)
		return err
	}

	// configure git credential helper
	if cmd.ConfigureGitHelper {
		binaryPath, err := os.Executable()
		if err != nil {
			return err
		}
		err = gitcredentials.ConfigureHelper(binaryPath, cmd.User, port)
		if err != nil {
			return fmt.Errorf("configure git helper: %w", err)
		}

		// cleanup when we are done
		defer func(userName string) {
			_ = gitcredentials.RemoveHelper(userName)
		}(cmd.User)
	}

	// configure git ssh signature helper -- non-fatal so that a signing
	// setup failure does not take down the entire credentials server
	// (git/docker credential forwarding, port forwarding, etc.)
	if cmd.GitUserSigningKey != "" {
		decodedKey, err := base64.StdEncoding.DecodeString(cmd.GitUserSigningKey)
		if err != nil {
			log.Errorf("Failed to decode git SSH signing key, signing will be unavailable: %v", err)
		} else {
			err = gitsshsigning.ConfigureHelper(cmd.User, string(decodedKey))
			if err != nil {
				log.Errorf(
					"Failed to configure git SSH signature helper, signing will be unavailable: %v",
					err,
				)
			} else {
				defer func(userName string) {
					_ = gitsshsigning.RemoveHelper(userName)
				}(cmd.User)
			}
		}
	}

	return credentials.RunCredentialsServer(ctx, port, tunnelClient)
}

func configureGitUserLocally(
	ctx context.Context,
	userName string,
	client tunnel.TunnelClient,
) error {
	// get local credentials
	localGitUser, err := gitcredentials.GetUser(userName)
	if err != nil {
		return err
	} else if localGitUser.Name != "" && localGitUser.Email != "" {
		return nil
	}

	// set user & email if not found
	response, err := client.GitUser(ctx, &tunnel.Empty{})
	if err != nil {
		return fmt.Errorf("retrieve git user: %w", err)
	}

	// parse git user from response
	gitUser := &gitcredentials.GitUser{}
	err = json.Unmarshal([]byte(response.Message), gitUser)
	if err != nil {
		return fmt.Errorf("decode git user: %w", err)
	}

	// don't override what is already there
	if localGitUser.Name != "" {
		gitUser.Name = ""
	}
	if localGitUser.Email != "" {
		gitUser.Email = ""
	}

	// set git user
	err = gitcredentials.SetUser(userName, gitUser)
	if err != nil {
		return fmt.Errorf("set git user & email: %w", err)
	}

	return nil
}

func forwardPorts(ctx context.Context, client tunnel.TunnelClient) error {
	opts := portOptionsFromResult()
	return netstat.NewWatcher(&forwarder{ctx: ctx, client: client}, opts...).Run(ctx)
}

func portOptionsFromResult() []netstat.WatcherOption {
	raw, err := os.ReadFile(config.DevContainerResultPath)
	if err != nil {
		log.Debugf("Could not read result for port attributes: %v", err)
		return nil
	}
	result := &devconfig.Result{}
	if err := json.Unmarshal(raw, result); err != nil {
		log.Debugf("Could not parse result for port attributes: %v", err)
		return nil
	}
	mc := result.MergedConfig
	if mc == nil || (len(mc.PortsAttributes) == 0 && mc.OtherPortsAttributes == nil) {
		return nil
	}
	pa, opa := mc.PortsAttributes, mc.OtherPortsAttributes
	resolver := func(port string) netstat.PortForwardAttribute {
		portNum, err := strconv.Atoi(port)
		if err != nil {
			return netstat.PortForwardAttribute{}
		}
		attr := devconfig.ResolvePortAttribute(portNum, pa, opa)
		return netstat.PortForwardAttribute{
			Label:         attr.Label,
			Protocol:      attr.Protocol,
			OnAutoForward: attr.OnAutoForward,
		}
	}
	return []netstat.WatcherOption{
		netstat.WithPortAttributes(resolver),
	}
}

type forwarder struct {
	ctx context.Context

	client tunnel.TunnelClient
}

func (f *forwarder) Forward(port string, attr netstat.PortForwardAttribute) error {
	if attr.Label != "" {
		log.Debugf("Forwarding port %s (%s, protocol=%s)", port, attr.Label, attr.Protocol)
	}
	_, err := f.client.ForwardPort(f.ctx, &tunnel.ForwardPortRequest{Port: port})
	return err
}

func (f *forwarder) StopForward(port string) error {
	_, err := f.client.StopForwardPort(f.ctx, &tunnel.StopForwardPortRequest{Port: port})
	return err
}
