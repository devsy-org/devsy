package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"golang.org/x/crypto/ssh"
)

// ErrIdleTimeout is returned by PortForward / ReversePortForward when the
// forwarder shuts down because it stayed idle longer than the configured
// EXIT_AFTER_TIMEOUT. Callers that want to treat idle-timeout as a clean exit
// should check for this error with errors.Is.
var ErrIdleTimeout = errors.New("port forward idle timeout")

var ErrTransportClosed = errors.New("ssh transport closed")

type forwardTarget struct {
	network string
	address string
}

type ForwardingFunction func(context.Context, net.Conn, *ssh.Client, forwardTarget)

func PortForward(
	ctx context.Context,
	client *ssh.Client,
	localNetwork, localAddr, remoteNetwork, remoteAddr string,
	exitAfterTimeout time.Duration,
) error {
	listener, err := net.Listen(localNetwork, localAddr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	return portForwarding(
		ctx, client, listener,
		localAddr, remoteNetwork, remoteAddr,
		exitAfterTimeout, forward,
	)
}

// ForwardOpts groups the parameters for PortForwardWithListener so callers
// don't have to thread a long positional argument list.
type ForwardOpts struct {
	Listener         net.Listener
	RemoteAddr       string
	ExitAfterTimeout time.Duration
}

// PortForwardWithListener is like PortForward, but uses a caller-supplied
// listener instead of binding one internally. This lets the caller reserve
// the local port before forking, eliminating a TOCTOU race between port
// probe and net.Listen in a detached child. The remote network is always
// "tcp".
func PortForwardWithListener(
	ctx context.Context,
	client *ssh.Client,
	opts ForwardOpts,
) error {
	defer func() { _ = opts.Listener.Close() }()

	return portForwarding(
		ctx, client, opts.Listener,
		opts.Listener.Addr().String(), "tcp", opts.RemoteAddr,
		opts.ExitAfterTimeout, forward,
	)
}

func ReversePortForward(
	ctx context.Context,
	client *ssh.Client,
	remoteNetwork, remoteAddr, localNetwork, localAddr string,
	exitAfterTimeout time.Duration,
) error {
	listener, err := ReverseListen(client, remoteNetwork, remoteAddr)
	if err != nil {
		return err
	}
	return RunReverseForward(ctx, client, ReverseForwardOpts{
		Listener:         listener,
		RemoteAddr:       remoteAddr,
		LocalNetwork:     localNetwork,
		LocalAddr:        localAddr,
		ExitAfterTimeout: exitAfterTimeout,
	})
}

// ReverseListen binds the remote listener; pair with RunReverseForward.
func ReverseListen(client *ssh.Client, remoteNetwork, remoteAddr string) (net.Listener, error) {
	return client.Listen(remoteNetwork, remoteAddr)
}

// ReverseForwardOpts groups the parameters for RunReverseForward.
type ReverseForwardOpts struct {
	Listener         net.Listener
	RemoteAddr       string
	LocalNetwork     string
	LocalAddr        string
	ExitAfterTimeout time.Duration
}

// RunReverseForward runs the forwarding loop for a listener obtained via
// ReverseListen, closing it on return.
func RunReverseForward(ctx context.Context, client *ssh.Client, opts ReverseForwardOpts) error {
	defer func() { _ = opts.Listener.Close() }()
	return portForwarding(
		ctx, client, opts.Listener,
		opts.RemoteAddr, opts.LocalNetwork, opts.LocalAddr,
		opts.ExitAfterTimeout, reverseForward,
	)
}

func portForwarding(
	ctx context.Context,
	client *ssh.Client,
	listener net.Listener,
	srcAddr, dstNetwork, dstAddr string,
	exitAfterTimeout time.Duration,
	forwardFn ForwardingFunction,
) error {
	// Derive a child context so the idle-timeout handler can signal shutdown
	// with a typed cause (ErrIdleTimeout) without killing the whole process.
	fwdCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-done:
		case <-fwdCtx.Done():
			_ = listener.Close()
		}
	}()

	watchTransportClosed(client, done, srcAddr, func() { cancel(ErrTransportClosed) })

	counter := newConnectionCounter(fwdCtx, exitAfterTimeout, func() {
		log.Infof(
			"Stopping port-forward on %s: idle for a while. "+
				"You can disable this via 'devsy context set -o EXIT_AFTER_TIMEOUT=false'",
			srcAddr,
		)
		cancel(ErrIdleTimeout)
	}, srcAddr)
	defer counter.Close()
	for {
		// waiting for a new connection
		connection, err := listener.Accept()
		if err != nil {
			switch cause := context.Cause(fwdCtx); {
			case errors.Is(cause, ErrIdleTimeout):
				return ErrIdleTimeout
			case errors.Is(cause, ErrTransportClosed):
				return ErrTransportClosed
			}
			return err
		}

		// tell the counter there is a connection
		if !counter.Add() {
			_ = connection.Close()
			continue
		}

		// forward connection
		go func() {
			defer counter.Dec()

			forwardFn(fwdCtx, connection, client, forwardTarget{
				network: dstNetwork,
				address: dstAddr,
			})
		}()
	}
}

// watchTransportClosed spawns a goroutine that waits for client's transport
// to close and invokes onClosed, unless done fires first. A nil client is a
// no-op.
func watchTransportClosed(
	client *ssh.Client, done <-chan struct{}, srcAddr string, onClosed func(),
) {
	if client == nil {
		return
	}
	transportClosed := make(chan struct{})
	go func() {
		if werr := client.Wait(); werr != nil {
			log.Debugf("ssh transport closed on %s: %v", srcAddr, werr)
		}
		close(transportClosed)
	}()
	go func() {
		select {
		case <-done:
		case <-transportClosed:
			onClosed()
		}
	}()
}

func forward(
	ctx context.Context,
	localConn net.Conn,
	client *ssh.Client,
	target forwardTarget,
) {
	defer func() { _ = localConn.Close() }()
	// Setup sshConn (type net.Conn)
	sshConn, err := client.Dial(target.network, target.address)
	if err != nil {
		log.Debugf("error dialing remote: %v", err)
		return
	}
	defer func() { _ = sshConn.Close() }()
	if err := relayDuplex(ctx, localConn, sshConn); err != nil {
		log.Debugf("error forwarding connection: %v", err)
	}
}

func reverseForward(
	ctx context.Context,
	remoteConn net.Conn,
	client *ssh.Client,
	target forwardTarget,
) {
	defer func() { _ = remoteConn.Close() }()
	// Setup localConn (type net.Conn)
	localConn, err := net.Dial(target.network, target.address)
	if err != nil {
		log.Debugf("error dialing remote: %v", err)
		return
	}
	defer func() { _ = localConn.Close() }()
	if err := relayDuplex(ctx, remoteConn, localConn); err != nil {
		log.Debugf("error forwarding reverse connection: %v", err)
	}
}

type closeWriter interface{ CloseWrite() error }

type relayResult struct {
	direction string
	err       error
}

func relayOneWay(dst net.Conn, src net.Conn, direction string, results chan<- relayResult) {
	_, err := io.Copy(dst, src)
	if err == nil {
		cw, ok := dst.(closeWriter)
		if !ok {
			err = errors.New("destination does not support CloseWrite")
		} else if closeErr := cw.CloseWrite(); closeErr != nil && !errors.Is(closeErr, io.EOF) {
			err = closeErr
		}
	}
	results <- relayResult{direction: direction, err: err}
}

func relayDuplex(ctx context.Context, left, right net.Conn) error {
	results := make(chan relayResult, 2)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = left.Close()
			_ = right.Close()
		case <-stop:
		}
	}()
	go relayOneWay(right, left, "local-to-remote", results)
	go relayOneWay(left, right, "remote-to-local", results)

	var firstErr error
	for range 2 {
		result := <-results
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s relay: %w", result.direction, result.err)
			_ = left.Close()
			_ = right.Close()
		}
	}
	return firstErr
}
