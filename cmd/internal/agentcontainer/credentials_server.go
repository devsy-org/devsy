package agentcontainer

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
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/gitsshsigning"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/netstat"
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
	cliflags.Add(
		credentialsServerCmd,
		cliflags.Bool(
			&cmd.ConfigureGitHelper,
			names.ConfigureGitHelper,
			false,
			"If true will configure git helper",
		),
		cliflags.Bool(
			&cmd.ConfigureDockerHelper,
			names.ConfigureDockerHelper,
			false,
			"If true will configure docker helper",
		),
		cliflags.Bool(
			&cmd.ForwardPorts,
			names.ForwardPorts,
			false,
			"If true will automatically try to forward open ports within the container",
		),
		cliflags.String(&cmd.GitUserSigningKey, names.GitUserSigningKey, "", ""),
		cliflags.String(&cmd.User, names.User, "", "The user to use"),
	)
	_ = credentialsServerCmd.MarkFlagRequired(names.User)

	return credentialsServerCmd
}

// Run runs the command logic.
func (cmd *CredentialsServerCmd) Run(ctx context.Context, port int) error {
	tunnelClient, err := tunnelserver.NewTunnelClient(os.Stdin, os.Stdout, true, ExitCodeIO)
	if err != nil {
		return fmt.Errorf("error creating tunnel client: %w", err)
	}

	if _, err := tunnelClient.Ping(ctx, &tunnel.Empty{}); err != nil {
		return fmt.Errorf("ping client: %w", err)
	}

	ln, err := claimPort(port)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	cmd.maybeForwardPorts(ctx, tunnelClient)

	if err := cmd.configureDockerHelper(port); err != nil {
		return err
	}

	if err := configureGitUserLocally(ctx, cmd.User, tunnelClient); err != nil {
		log.Warnf("error configuring git user: %v", err)
		return err
	}

	cleanupGitHelper, err := cmd.configureGitCredentialHelper(ctx, port)
	if err != nil {
		return err
	}
	defer cleanupGitHelper()

	cleanupGitSigning := cmd.configureGitSigningKey()
	defer cleanupGitSigning()

	return credentials.RunCredentialsServerWithListener(ctx, ln, tunnelClient)
}

// claimPort binds port and returns the listener, holding it exclusively so
// no other session can bind the same port until the caller closes it (or
// hands it to RunCredentialsServerWithListener). Only one session's
// credentials-server can hold this port at a time. Returning an error (not
// nil) on contention matters: RunServices (pkg/tunnel/services.go) wraps
// this command in retry.OnError, which only retries on a non-nil error.
func claimPort(port int) (net.Listener, error) {
	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf(
			"port %d not available (another session may own the credentials server): %w",
			port,
			err,
		)
	}
	return ln, nil
}

func (cmd *CredentialsServerCmd) maybeForwardPorts(
	ctx context.Context,
	tunnelClient tunnel.TunnelClient,
) {
	if !cmd.ForwardPorts {
		return
	}
	go func() {
		log.Debugf("start watching & forwarding open ports")
		if err := forwardPorts(ctx, tunnelClient); err != nil {
			log.Errorf("error forwarding ports: %v", err)
		}
	}()
}

func (cmd *CredentialsServerCmd) configureDockerHelper(port int) error {
	if !cmd.ConfigureDockerHelper {
		return nil
	}
	return dockercredentials.ConfigureCredentialsContainer(cmd.User, port)
}

func (cmd *CredentialsServerCmd) configureGitCredentialHelper(
	ctx context.Context,
	port int,
) (func(), error) {
	noop := func() {}
	if !cmd.ConfigureGitHelper {
		return noop, nil
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return noop, err
	}
	if err := gitcredentials.ConfigureHelper(ctx, binaryPath, cmd.User, port); err != nil {
		return noop, fmt.Errorf("configure git helper: %w", err)
	}

	// cleanup when we are done. This defer runs after the server loop
	// returns on shutdown, when ctx is already canceled — use an uncanceled
	// context so the helper is actually removed instead of aborting early.
	cleanupCtx := context.WithoutCancel(ctx)
	userName := cmd.User
	return func() {
		_ = gitcredentials.RemoveHelper(cleanupCtx, userName)
	}, nil
}

func (cmd *CredentialsServerCmd) configureGitSigningKey() func() {
	noop := func() {}
	if cmd.GitUserSigningKey == "" {
		return noop
	}

	decodedKey, err := base64.StdEncoding.DecodeString(cmd.GitUserSigningKey)
	if err != nil {
		log.Errorf("Failed to decode git SSH signing key, signing will be unavailable: %v", err)
		return noop
	}

	if err := gitsshsigning.ConfigureHelper(cmd.User, string(decodedKey)); err != nil {
		log.Errorf(
			"Failed to configure git SSH signature helper, signing will be unavailable: %v",
			err,
		)
		return noop
	}

	userName := cmd.User
	return func() {
		_ = gitsshsigning.RemoveHelper(userName)
	}
}

func configureGitUserLocally(
	ctx context.Context,
	userName string,
	client tunnel.TunnelClient,
) error {
	// get local credentials
	localGitUser, err := gitcredentials.GetUser(ctx, userName, "")
	if err != nil {
		return err
	}
	if localGitUser.Name != "" && localGitUser.Email != "" {
		return nil
	}

	// set user & email if not found
	gitUser, err := fetchRemoteGitUser(ctx, client)
	if err != nil {
		return err
	}

	// don't override what is already there
	clearKnownGitUserFields(localGitUser, gitUser)

	// set git user
	if err := gitcredentials.SetUser(ctx, userName, gitUser); err != nil {
		return fmt.Errorf("set git user & email: %w", err)
	}

	return nil
}

func fetchRemoteGitUser(
	ctx context.Context,
	client tunnel.TunnelClient,
) (*gitcredentials.GitUser, error) {
	response, err := client.GitUser(ctx, &tunnel.Empty{})
	if err != nil {
		return nil, fmt.Errorf("retrieve git user: %w", err)
	}

	gitUser := &gitcredentials.GitUser{}
	if err := json.Unmarshal([]byte(response.Message), gitUser); err != nil {
		return nil, fmt.Errorf("decode git user: %w", err)
	}

	return gitUser, nil
}

func clearKnownGitUserFields(local, remote *gitcredentials.GitUser) {
	if local.Name != "" {
		remote.Name = ""
	}
	if local.Email != "" {
		remote.Email = ""
	}
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
			Label:            attr.Label,
			Protocol:         attr.Protocol,
			OnAutoForward:    attr.OnAutoForward,
			RequireLocalPort: attr.RequireLocalPort,
			ElevateIfNeeded:  attr.ElevateIfNeeded,
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
