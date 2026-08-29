package ts

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"golang.org/x/crypto/ssh"
)

type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// SSHDialConfig bundles the parameters needed to establish and retry an SSH
// connection over a tailnet dialer.
type SSHDialConfig struct {
	Dialer  Dialer
	Network string
	Address string
	User    string
	Timeout time.Duration
}

func WaitForSSHClient(ctx context.Context, cfg SSHDialConfig) (*ssh.Client, error) {
	deadline := time.Now().Add(cfg.Timeout)

	var (
		c   *ssh.Client
		err error
	)
	log.Debugf("attempting to establish SSH connection with %s as user %s", cfg.Address, cfg.User)
	for time.Now().Before(deadline) {
		c, err = newSSHClient(ctx, cfg)
		if err == nil {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return c, err
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
	log.Debugf("failed to establish SSH connection %v", err)

	return c, err
}

func newSSHClient(ctx context.Context, cfg SSHDialConfig) (*ssh.Client, error) {
	conn, err := cfg.Dialer(ctx, cfg.Network, cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.Address, err)
	}

	clientConfig := &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{}, // The SSH server is only reachable through the tailnet
		// #nosec G106 -- the tailnet, not TLS/host-key trust, is this connection's security boundary
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	sshConn, channels, requests, err := ssh.NewClientConn(conn, cfg.Address, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("establish SSH connection: %w", err)
	}

	return ssh.NewClient(sshConn, channels, requests), nil
}
