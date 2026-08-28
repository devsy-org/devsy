package daemonclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	"github.com/devsy-org/devsy/pkg/log"
	devsyopen "github.com/devsy-org/devsy/pkg/open"
	"github.com/devsy-org/devsy/pkg/options"
	"github.com/devsy-org/devsy/pkg/options/resolver"
	"github.com/devsy-org/devsy/pkg/platform"
	platformclient "github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
	sshServer "github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/devsy-org/devsy/pkg/ts"
	"golang.org/x/crypto/ssh"
	"tailscale.com/client/local"
	"tailscale.com/tailcfg"
)

func New(
	devsyConfig *config.Config,
	prov *provider.ProviderConfig,
	workspace *provider.Workspace,
) (clientpkg.DaemonClient, error) {
	tsClient := &local.Client{
		Socket:        daemon.GetSocketAddr(workspace.Provider.Name),
		UseSocketOnly: true,
	}

	return &client{
		devsyConfig: devsyConfig,
		config:      prov,
		workspace:   workspace,
		tsClient:    tsClient,
		localClient: daemon.NewLocalClient(prov.Name),
	}, nil
}

type client struct {
	m sync.Mutex

	devsyConfig *config.Config
	config      *provider.ProviderConfig
	workspace   *provider.Workspace
	tsClient    *local.Client
	localClient *daemon.LocalClient
}

func (c *client) Lock(ctx context.Context) error {
	// noop
	return nil
}

func (c *client) Unlock() {
	// noop
}

func (c *client) Provider() string {
	return c.config.Name
}

func (c *client) Workspace() string {
	c.m.Lock()
	defer c.m.Unlock()

	return c.workspace.ID
}

func (c *client) WorkspaceConfig() *provider.Workspace {
	c.m.Lock()
	defer c.m.Unlock()

	return provider.CloneWorkspace(c.workspace)
}

func (c *client) Context() string {
	return c.workspace.Context
}

func (c *client) RefreshOptions(
	ctx context.Context,
	userOptionsRaw []string,
	reconfigure bool,
) error {
	c.m.Lock()
	defer c.m.Unlock()

	userOptions, err := provider.ParseOptions(userOptionsRaw)
	if err != nil {
		return fmt.Errorf("parse options: %w", err)
	}

	workspace, err := options.ResolveAndSaveOptionsWorkspace(
		ctx,
		c.devsyConfig,
		c.config,
		c.workspace,
		userOptions,
		resolver.WithResolveSubOptions(),
	)
	if err != nil {
		return err
	}

	if reconfigure {
		err := c.updateInstance(ctx)
		if err != nil {
			return err
		}
	}

	c.workspace = workspace
	return nil
}

func (c *client) CheckWorkspaceReachable(ctx context.Context) error {
	wAddr, err := c.getWorkspaceAddress()
	if err != nil {
		return fmt.Errorf("resolve workspace hostname: %w", err)
	}
	err = ts.WaitHostReachable(ctx, c.tsClient, wAddr, 5)
	if err == nil {
		log.Debugf("Host %s is reachable. Proceeding with SSH session", wAddr.Host())
		return nil
	}

	instance, getWorkspaceErr := c.localClient.GetWorkspace(ctx, c.workspace.UID)
	// if we can't reach the daemon try to start the desktop app
	if daemon.IsDaemonNotAvailableError(getWorkspaceErr) {
		openErr := c.openDesktopApp()
		if openErr != nil {
			return getWorkspaceErr // inform user about daemon state
		}
		// give desktop app a chance to start
		time.Sleep(2 * time.Second)

		// let's try again
		err = ts.WaitHostReachable(ctx, c.tsClient, wAddr, 20)
		if err == nil {
			return nil
		}
		instance, getWorkspaceErr = c.localClient.GetWorkspace(ctx, c.workspace.UID)
	}

	return c.workspaceUnreachableError(instance, getWorkspaceErr, err)
}

func (c *client) SSHClients(
	ctx context.Context,
	user string,
) (toolClient *ssh.Client, userClient *ssh.Client, err error) {
	wAddr, err := c.getWorkspaceAddress()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve workspace hostname: %w", err)
	}

	address := fmt.Sprintf("%s:%d", wAddr.Host(), wAddr.Port())
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		addressParts := strings.Split(address, ":")
		if len(addressParts) != 2 {
			return nil, fmt.Errorf("invalid address: %s", address)
		}

		port, err := strconv.Atoi(addressParts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid port: %s", addressParts[1])
		}
		port16, err := ts.ToUint16Port(port)
		if err != nil {
			return nil, fmt.Errorf("invalid port: %w", err)
		}

		return c.tsClient.DialTCP(ctx, addressParts[0], port16)
	}

	toolClient, err = ts.WaitForSSHClient(ctx, ts.SSHDialConfig{
		Dialer: dial, Network: "tcp", Address: address, User: "root", Timeout: time.Second * 10,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create SSH tool client: %w", err)
	}
	userClient, err = ts.WaitForSSHClient(ctx, ts.SSHDialConfig{
		Dialer: dial, Network: "tcp", Address: address, User: user, Timeout: time.Second * 10,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create SSH user client: %w", err)
	}

	return toolClient, userClient, nil
}

func (c *client) DirectTunnel(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	wAddr, err := c.getWorkspaceAddress()
	if err != nil {
		return fmt.Errorf("resolve workspace hostname: %w", err)
	}
	port, err := wAddr.PortUint16()
	if err != nil {
		return fmt.Errorf("invalid workspace port: %w", err)
	}
	conn, err := c.tsClient.DialTCP(ctx, wAddr.Host(), port)
	if err != nil {
		return fmt.Errorf("failed to connect to SSH server in proxy mode: %w", err)
	}
	defer func() { _ = conn.Close() }()

	errChan := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdout, conn)
		errChan <- err
	}()
	go func() {
		_, err := io.Copy(conn, stdin)
		errChan <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}

func (c *client) Ping(ctx context.Context, writer io.Writer) error {
	wAddr, err := c.getWorkspaceAddress()
	if err != nil {
		return err
	}
	status, err := c.tsClient.Status(ctx)
	if err != nil {
		return err
	}
	hostname := strings.TrimSuffix(wAddr.Host(), "."+status.CurrentTailnet.Name)
	var ip *netip.Addr
	for _, peer := range status.Peer {
		if peer.HostName == hostname {
			ip = &peer.TailscaleIPs[0]
		}
	}

	if ip == nil {
		return fmt.Errorf("no network peer for hostname %s", wAddr.Host())
	}

	for range 10 {
		if err := c.pingOnce(ctx, *ip, writer); err != nil {
			return err
		}

		time.Sleep(time.Second)
	}

	return nil
}

func (c *client) pingOnce(ctx context.Context, ip netip.Addr, writer io.Writer) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := c.tsClient.Ping(timeoutCtx, ip, tailcfg.PingDisco)
	if err != nil {
		return err
	}
	if result.Err != "" {
		return errors.New(result.Err)
	}
	latency := time.Duration(result.LatencySeconds * float64(time.Second)).
		Round(time.Millisecond)
	via := result.Endpoint
	if result.DERPRegionID != 0 {
		via = fmt.Sprintf("DERP(%s)", result.DERPRegionCode)
	}
	_, err = fmt.Fprintf(
		writer,
		"pong from %s (%s) via %v in %v\n",
		result.NodeName,
		result.NodeIP,
		via,
		latency,
	)
	if err != nil {
		return fmt.Errorf("failed to write ping result: %w", err)
	}

	return nil
}

func (c *client) initPlatformClient(ctx context.Context) (platformclient.Client, error) {
	configPath, err := platform.DevsyConfigPath(c.Context(), c.Provider())
	if err != nil {
		return nil, err
	}
	baseClient, err := platformclient.InitClientFromPath(ctx, configPath)
	if err != nil {
		return nil, err
	}

	return baseClient, nil
}

func (c *client) getWorkspaceAddress() (ts.Addr, error) {
	if c.workspace.Pro == nil || c.workspace.Pro.InstanceName == "" {
		return ts.Addr{}, fmt.Errorf("workspace is not initialized")
	}

	return ts.NewAddr(
		ts.GetWorkspaceHostname(c.workspace.Pro.InstanceName, c.workspace.Pro.Project),
		sshServer.DefaultUserPort,
	), nil
}

func (c *client) openDesktopApp() error {
	deeplink := fmt.Sprintf(
		"devsy://open?workspace=%s&provider=%s&source=%s&ide=%s",
		c.workspace.ID,
		c.config.Name,
		c.workspace.Source.String(),
		c.workspace.IDE.Name,
	)

	return devsyopen.Run(deeplink)
}

func (c *client) workspaceUnreachableError(
	instance *managementv1.DevsyWorkspaceInstance,
	getWorkspaceErr, reachErr error,
) error {
	switch {
	case getWorkspaceErr != nil:
		return fmt.Errorf("couldn't get workspace: %w", getWorkspaceErr)
	case instance == nil:
		return fmt.Errorf(
			"workspace not found, run `devsy workspace up %s` to start it again",
			c.workspace.ID,
		)
	case instance.Status.Phase != storagev1.InstanceReady:
		return fmt.Errorf(
			"workspace is %q, run `devsy workspace up %s` to start it again",
			instance.Status.Phase,
			c.workspace.ID,
		)
	case instance.Status.LastWorkspaceStatus != storagev1.WorkspaceStatusRunning:
		return fmt.Errorf(
			"workspace is %q, run `devsy workspace up %s` to start it again",
			instance.Status.LastWorkspaceStatus,
			c.workspace.ID,
		)
	}

	return fmt.Errorf("reach host: %w", reachErr)
}
