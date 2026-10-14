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

	log.Infof("using port forwarding timeout of %s", cmd.ForwardPortsTimeout)
	return timeout, nil
}

// forwardPortsIfRequested handles -L/-R forwarding when requested. The returned
// bool reports whether forwarding took over (the caller should return err).
// -L and -R together are not supported.
func (cmd *SSHCmd) forwardPortsIfRequested(
	ctx context.Context,
	sshClient *ssh.Client,
) (bool, error) {
	hasForward := len(cmd.ForwardPorts) > 0
	hasReverse := len(cmd.ReverseForwardPorts) > 0 && !cmd.GPGAgentForwarding
	if hasForward && hasReverse {
		return true, fmt.Errorf("-L and -R cannot be combined in a single ssh invocation")
	}
	if hasForward {
		return true, cmd.forwardPorts(ctx, sshClient)
	}
	if hasReverse {
		return true, cmd.reverseForwardPorts(ctx, sshClient)
	}
	return false, nil
}

// forwardDirection abstracts the one difference between forwardPorts and
// reverseForwardPorts: which devssh function establishes each connection and
// how log messages describe it.
type forwardDirection struct {
	logPrefix string // e.g. "Forwarding" or "Reverse forwarding"
	logLabel  string // e.g. "port-forward" or "reverse port-forward"
	forward   func(ctx context.Context, client *ssh.Client, mapping port.Mapping, timeout time.Duration) error
}

var (
	directionForward = forwardDirection{
		logPrefix: "Forwarding",
		logLabel:  "port-forward",
		forward: func(ctx context.Context, client *ssh.Client, mapping port.Mapping, timeout time.Duration) error {
			return devssh.PortForward(
				ctx, client,
				mapping.Host.Protocol, mapping.Host.Address,
				mapping.Container.Protocol, mapping.Container.Address,
				timeout,
			)
		},
	}
	directionReverse = forwardDirection{
		logPrefix: "Reverse forwarding",
		logLabel:  "reverse port-forward",
		forward: func(ctx context.Context, client *ssh.Client, mapping port.Mapping, timeout time.Duration) error {
			return devssh.ReversePortForward(
				ctx, client,
				mapping.Host.Protocol, mapping.Host.Address,
				mapping.Container.Protocol, mapping.Container.Address,
				timeout,
			)
		},
	}
)

// portForwardRun bundles a single forwardPorts/reverseForwardPorts
// invocation's inputs so runPortForwards stays within the linter's
// argument-count limit.
type portForwardRun struct {
	portMappings []string
	timeout      time.Duration
	dir          forwardDirection
}

func (cmd *SSHCmd) forwardPorts(ctx context.Context, containerClient *ssh.Client) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return err
	}
	return runPortForwards(ctx, containerClient, portForwardRun{
		portMappings: cmd.ForwardPorts,
		timeout:      timeout,
		dir:          directionForward,
	})
}

func (cmd *SSHCmd) reverseForwardPorts(ctx context.Context, containerClient *ssh.Client) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return err
	}
	return runPortForwards(ctx, containerClient, portForwardRun{
		portMappings: cmd.ReverseForwardPorts,
		timeout:      timeout,
		dir:          directionReverse,
	})
}

// runPortForwards starts one forwarding goroutine per mapping and blocks
// until every mapping has exited cleanly (idle timeout or EOF), one reports
// a real error, or ctx is done. A single mapping's clean exit doesn't tear
// down the others: with multiple -L/-R mappings, an idle timeout on one
// shouldn't end the whole session while its siblings are still useful.
func runPortForwards(ctx context.Context, containerClient *ssh.Client, run portForwardRun) error {
	errChan := make(chan error, len(run.portMappings))
	for _, portMapping := range run.portMappings {
		mapping, err := port.ParsePortSpec(portMapping)
		if err != nil {
			return fmt.Errorf("parse port mapping: %w", err)
		}

		log.Infof(
			"%s local %s/%s to remote %s/%s",
			run.dir.logPrefix,
			mapping.Host.Protocol,
			mapping.Host.Address,
			mapping.Container.Protocol,
			mapping.Container.Address,
		)
		go runPortForward(ctx, containerClient, singlePortForward{
			dir:         run.dir,
			timeout:     run.timeout,
			portMapping: portMapping,
			mapping:     mapping,
			errChan:     errChan,
		})
	}

	for range run.portMappings {
		select {
		case err := <-errChan:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// singlePortForward bundles a single runPortForward invocation's inputs so
// it stays within the linter's argument-count limit.
type singlePortForward struct {
	dir         forwardDirection
	timeout     time.Duration
	portMapping string
	mapping     port.Mapping
	errChan     chan<- error
}

// runPortForward runs a single mapping's forward and reports its outcome on
// f.errChan: nil for a clean exit (idle timeout or EOF), the wrapped error
// otherwise.
func runPortForward(ctx context.Context, containerClient *ssh.Client, f singlePortForward) {
	err := f.dir.forward(ctx, containerClient, f.mapping, f.timeout)
	if errors.Is(err, devssh.ErrIdleTimeout) {
		log.Infof("%s %s exited due to idle timeout", f.dir.logLabel, f.portMapping)
		f.errChan <- nil
		return
	}
	if err == nil || errors.Is(err, io.EOF) {
		f.errChan <- nil
		return
	}
	f.errChan <- fmt.Errorf("error forwarding %s: %w", f.portMapping, err)
}

type boundReverseForward struct {
	portMapping string
	mapping     port.Mapping
	listener    net.Listener
}

// managedReverseForward owns the GPG socket's reverse-forward loop. done
// receives its terminal result after the listener has been closed.
type managedReverseForward struct {
	cancel context.CancelFunc
	done   <-chan error
}

type managedReverseForwardRun struct {
	ctx      context.Context
	cancel   context.CancelFunc
	client   *ssh.Client
	forward  boundReverseForward
	timeout  time.Duration
	doneChan chan<- error
}

// startReverseForwardsAndWait returns after every forward's listener is bound,
// unlike reverseForwardPorts which blocks for the forward's lifetime. The
// returned handle lets the GPG tunnel observe its first mapping's lifecycle;
// any additional mappings retain their independent lifetime.
func (cmd *SSHCmd) startReverseForwardsAndWait(
	ctx context.Context,
	containerClient *ssh.Client,
	portMappings []string,
) (*managedReverseForward, error) {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return nil, err
	}

	bound, err := bindReverseForwards(containerClient, portMappings)
	if err != nil {
		return nil, err
	}

	for _, b := range bound {
		log.Infof(
			"reverse forwarding local %s/%s to remote %s/%s",
			b.mapping.Host.Protocol,
			b.mapping.Host.Address,
			b.mapping.Container.Protocol,
			b.mapping.Container.Address,
		)
	}

	forwardCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go runManagedReverseForward(managedReverseForwardRun{
		ctx:      forwardCtx,
		cancel:   cancel,
		client:   containerClient,
		forward:  bound[0],
		timeout:  timeout,
		doneChan: done,
	})
	for _, b := range bound[1:] {
		go runReverseForwardInBackground(ctx, containerClient, b, timeout)
	}

	return &managedReverseForward{cancel: cancel, done: done}, nil
}

func runManagedReverseForward(run managedReverseForwardRun) {
	defer close(run.doneChan)
	err := devssh.RunReverseForward(run.ctx, run.client, devssh.ReverseForwardOpts{
		Listener:         run.forward.listener,
		RemoteAddr:       run.forward.mapping.Host.Address,
		LocalNetwork:     run.forward.mapping.Container.Protocol,
		LocalAddr:        run.forward.mapping.Container.Address,
		ExitAfterTimeout: run.timeout,
	})
	if err != nil && !errors.Is(err, devssh.ErrIdleTimeout) && !errors.Is(err, io.EOF) &&
		!errors.Is(err, context.Canceled) {
		log.Errorf("error forwarding %s: %v", run.forward.portMapping, err)
		err = fmt.Errorf("error forwarding %s: %w", run.forward.portMapping, err)
	}
	run.cancel()
	run.doneChan <- err
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
