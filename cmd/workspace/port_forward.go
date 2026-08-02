package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/port"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

func (cmd *SSHCmd) forwardTimeout() (time.Duration, error) {
	if cmd.ForwardPortsTimeout == "" {
		return 0, nil
	}

	timeout, err := time.ParseDuration(cmd.ForwardPortsTimeout)
	if err != nil {
		return 0, fmt.Errorf("parse forward ports timeout: %w", err)
	}

	log.Infof("Using port forwarding timeout of %s", cmd.ForwardPortsTimeout)
	return timeout, nil
}

// forwardPortsIfRequested handles -L/-R forwarding when requested. The returned
// bool reports whether forwarding took over (the caller should return err).
func (cmd *SSHCmd) forwardPortsIfRequested(
	ctx context.Context,
	sshClient *ssh.Client,
) (bool, error) {
	if len(cmd.ForwardPorts) > 0 {
		return true, cmd.forwardPorts(ctx, sshClient)
	}
	if len(cmd.ReverseForwardPorts) > 0 && !cmd.GPGAgentForwarding {
		return true, cmd.reverseForwardPorts(ctx, sshClient)
	}
	return false, nil
}

func (cmd *SSHCmd) forwardPorts(
	ctx context.Context,
	containerClient *ssh.Client,
) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return fmt.Errorf("parse forward ports timeout: %w", err)
	}

	errChan := make(chan error, len(cmd.ForwardPorts))
	for _, portMapping := range cmd.ForwardPorts {
		mapping, err := port.ParsePortSpec(portMapping)
		if err != nil {
			return fmt.Errorf("parse port mapping: %w", err)
		}

		// start the forwarding
		log.Infof(
			"Forwarding local %s/%s to remote %s/%s",
			mapping.Host.Protocol,
			mapping.Host.Address,
			mapping.Container.Protocol,
			mapping.Container.Address,
		)
		go func(portMapping string) {
			err := devssh.PortForward(
				ctx,
				containerClient,
				mapping.Host.Protocol,
				mapping.Host.Address,
				mapping.Container.Protocol,
				mapping.Container.Address,
				timeout,
			)
			if errors.Is(err, devssh.ErrIdleTimeout) {
				log.Infof("port-forward %s exited due to idle timeout", portMapping)
				errChan <- nil
				return
			}
			if err == nil || errors.Is(err, io.EOF) {
				errChan <- nil
				return
			}
			errChan <- fmt.Errorf("error forwarding %s: %w", portMapping, err)
		}(portMapping)
	}

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (cmd *SSHCmd) reverseForwardPorts(
	ctx context.Context,
	containerClient *ssh.Client,
) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return fmt.Errorf("parse forward ports timeout: %w", err)
	}

	errChan := make(chan error, len(cmd.ReverseForwardPorts))
	for _, portMapping := range cmd.ReverseForwardPorts {
		mapping, err := port.ParsePortSpec(portMapping)
		if err != nil {
			return fmt.Errorf("parse port mapping: %w", err)
		}

		// start the forwarding
		log.Infof(
			"Reverse forwarding local %s/%s to remote %s/%s",
			mapping.Host.Protocol,
			mapping.Host.Address,
			mapping.Container.Protocol,
			mapping.Container.Address,
		)
		go func(portMapping string) {
			err := devssh.ReversePortForward(
				ctx,
				containerClient,
				mapping.Host.Protocol,
				mapping.Host.Address,
				mapping.Container.Protocol,
				mapping.Container.Address,
				timeout,
			)
			if errors.Is(err, devssh.ErrIdleTimeout) {
				log.Infof("reverse port-forward %s exited due to idle timeout", portMapping)
				errChan <- nil
				return
			}
			if err == nil || errors.Is(err, io.EOF) {
				errChan <- nil
				return
			}
			errChan <- fmt.Errorf("error forwarding %s: %w", portMapping, err)
		}(portMapping)
	}

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type boundReverseForward struct {
	portMapping string
	mapping     port.Mapping
	listener    net.Listener
}

// startReverseForwardsAndWait blocks until every forward's listener is bound,
// unlike reverseForwardPorts which blocks for the forward's lifetime.
func (cmd *SSHCmd) startReverseForwardsAndWait(
	ctx context.Context,
	containerClient *ssh.Client,
	portMappings []string,
) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return err
	}

	bound, err := bindReverseForwards(containerClient, portMappings)
	if err != nil {
		return err
	}

	for _, b := range bound {
		log.Infof(
			"Reverse forwarding local %s/%s to remote %s/%s",
			b.mapping.Host.Protocol,
			b.mapping.Host.Address,
			b.mapping.Container.Protocol,
			b.mapping.Container.Address,
		)
		go runReverseForwardInBackground(ctx, containerClient, b, timeout)
	}

	return nil
}

func bindReverseForwards(
	containerClient *ssh.Client,
	portMappings []string,
) ([]boundReverseForward, error) {
	var bound []boundReverseForward
	closeBound := func() {
		for _, b := range bound {
			_ = b.listener.Close()
		}
	}
	for _, portMapping := range portMappings {
		mapping, err := port.ParsePortSpec(portMapping)
		if err != nil {
			closeBound()
			return nil, fmt.Errorf("parse port mapping: %w", err)
		}

		listener, err := devssh.ReverseListen(
			containerClient,
			mapping.Host.Protocol,
			mapping.Host.Address,
		)
		if err != nil {
			closeBound()
			return nil, fmt.Errorf("listen for reverse forward %s: %w", portMapping, err)
		}
		bound = append(bound, boundReverseForward{portMapping, mapping, listener})
	}
	return bound, nil
}

func runReverseForwardInBackground(
	ctx context.Context,
	containerClient *ssh.Client,
	b boundReverseForward,
	timeout time.Duration,
) {
	err := devssh.RunReverseForward(ctx, containerClient, devssh.ReverseForwardOpts{
		Listener:         b.listener,
		RemoteAddr:       b.mapping.Host.Address,
		LocalNetwork:     b.mapping.Container.Protocol,
		LocalAddr:        b.mapping.Container.Address,
		ExitAfterTimeout: timeout,
	})
	if err != nil && !errors.Is(err, devssh.ErrIdleTimeout) && !errors.Is(err, io.EOF) {
		log.Errorf("error forwarding %s: %v", b.portMapping, err)
	}
}
