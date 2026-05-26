package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"golang.org/x/crypto/ssh"
)

// ErrIdleTimeout is returned by PortForward / ReversePortForward when the
// forwarder shuts down because it stayed idle longer than the configured
// EXIT_AFTER_TIMEOUT. Callers that want to treat idle-timeout as a clean exit
// should check for this error with errors.Is.
var ErrIdleTimeout = errors.New("port forward idle timeout")

type ForwardingFunction func(
	net.Conn,
	*ssh.Client,
	string,
	string,
)

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
	listener, err := client.Listen(remoteNetwork, remoteAddr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	return portForwarding(
		ctx, client, listener,
		remoteAddr, localNetwork, localAddr,
		exitAfterTimeout, reverseForward,
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

	counter := newConnectionCounter(fwdCtx, exitAfterTimeout, func() {
		log.Infof(
			"Stopping port-forward on %s: idle for a while. "+
				"You can disable this via 'devsy context set-options -o EXIT_AFTER_TIMEOUT=false'",
			srcAddr,
		)
		cancel(ErrIdleTimeout)
	}, srcAddr)
	for {
		// waiting for a new connection
		connection, err := listener.Accept()
		if err != nil {
			// If shutdown was caused by the idle timeout, surface that
			// typed error so callers can choose to treat it as a clean exit.
			if cause := context.Cause(fwdCtx); errors.Is(cause, ErrIdleTimeout) {
				return ErrIdleTimeout
			}
			return err
		}

		// tell the counter there is a connection
		counter.Add()

		// forward connection
		go func() {
			defer counter.Dec()

			forwardFn(connection, client, dstNetwork, dstAddr)
		}()
	}
}

func forward(
	localConn net.Conn,
	client *ssh.Client,
	remoteNetwork, remoteAddr string,
) {
	// Setup sshConn (type net.Conn)
	sshConn, err := client.Dial(remoteNetwork, remoteAddr)
	if err != nil {
		log.Debugf("error dialing remote: %v", err)
		return
	}
	defer func() { _ = sshConn.Close() }()

	// Copy localConn.Reader to sshConn.Writer
	waitGroup := sync.WaitGroup{}
	waitGroup.Go(func() {
		defer func() { _ = sshConn.Close() }()

		_, err = io.Copy(sshConn, localConn)
		if err != nil {
			log.Debugf("error copying to remote: %v", err)
		}
	})

	// Copy sshConn.Reader to localConn.Writer
	waitGroup.Go(func() {
		defer func() { _ = localConn.Close() }()

		_, err = io.Copy(localConn, sshConn)
		if err != nil {
			log.Debugf("error copying to local: %v", err)
		}
	})
	waitGroup.Wait()
}

func reverseForward(
	remoteConn net.Conn,
	client *ssh.Client,
	localNetwork, localAddr string,
) {
	// Setup localConn (type net.Conn)
	localConn, err := net.Dial(localNetwork, localAddr)
	if err != nil {
		log.Debugf("error dialing remote: %v", err)
		return
	}
	defer func() { _ = localConn.Close() }()

	// Copy localConn.Reader to sshConn.Writer
	waitGroup := sync.WaitGroup{}
	waitGroup.Go(func() {
		defer func() { _ = localConn.Close() }()

		_, err = io.Copy(localConn, remoteConn)
		if err != nil {
			log.Debugf("error copying to local: %v", err)
		}
	})

	// Copy sshConn.Reader to localConn.Writer
	waitGroup.Go(func() {
		defer func() { _ = remoteConn.Close() }()

		_, err = io.Copy(remoteConn, localConn)
		if err != nil {
			log.Debugf("error copying to remote: %v", err)
		}
	})
	waitGroup.Wait()
}
