package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"golang.org/x/crypto/ssh"
)

type CloseReason string

const (
	CloseUnknown           CloseReason = "unknown"
	ClosePeerEOF           CloseReason = "peer_eof"
	CloseContextCancelled  CloseReason = "context_cancelled"
	CloseLocalShutdown     CloseReason = "local_shutdown"
	CloseProviderExit      CloseReason = "provider_exit"
	CloseProviderError     CloseReason = "provider_error"
	CloseSSHTransportError CloseReason = "ssh_transport_error"
	CloseSessionExit       CloseReason = "session_exit"
	CloseKeepaliveTimeout  CloseReason = "keepalive_timeout"
)

type Side string

const (
	SideUnknown  Side = "unknown"
	SideProvider Side = "provider"
	SideSSH      Side = "ssh"
	SideParent   Side = "parent"
)

const (
	TransportSideUnknown  = SideUnknown
	TransportSideProvider = SideProvider
	TransportSideSSH      = SideSSH
	TransportSideParent   = SideParent
)
const DefaultJoinTimeout = 5 * time.Second

const (
	TransportCloseUnknown           = CloseUnknown
	TransportClosePeerEOF           = ClosePeerEOF
	TransportCloseContextCancelled  = CloseContextCancelled
	TransportCloseLocalShutdown     = CloseLocalShutdown
	TransportCloseProviderExit      = CloseProviderExit
	TransportCloseProviderError     = CloseProviderError
	TransportCloseSSHTransportError = CloseSSHTransportError
	TransportCloseSessionExit       = CloseSessionExit
	TransportCloseKeepaliveTimeout  = CloseKeepaliveTimeout
)

type CloseInfo struct {
	Reason CloseReason
	Err    error
	Side   Side
}

type PersistentLifecycle struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	mu     sync.RWMutex
	info   CloseInfo
}

// NewPersistentLifecycle derives a cancellable context from parent and
// returns it alongside the lifecycle that controls it.
func NewPersistentLifecycle(parent context.Context) (*PersistentLifecycle, context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	lifecycle := &PersistentLifecycle{
		cancel: cancel, done: make(chan struct{}),
		info: CloseInfo{Reason: CloseUnknown, Side: SideUnknown},
	}
	return lifecycle, ctx
}

func (l *PersistentLifecycle) Done() <-chan struct{} { return l.done }

func (l *PersistentLifecycle) Close(info CloseInfo) {
	l.once.Do(func() {
		l.mu.Lock()
		l.info = info
		l.mu.Unlock()
		l.cancel()
		close(l.done)
	})
}

func (l *PersistentLifecycle) CloseInfo() CloseInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.info
}

func Classify(side Side, err error, parent context.Context) CloseInfo {
	if parent != nil && parent.Err() != nil {
		return CloseInfo{Reason: CloseContextCancelled, Side: SideParent, Err: parent.Err()}
	}
	if err == nil {
		return classifyNil(side)
	}
	if errors.Is(err, io.EOF) {
		return CloseInfo{Reason: ClosePeerEOF, Side: side, Err: err}
	}
	return classifyError(side, err)
}

func classifyNil(side Side) CloseInfo {
	switch side {
	case SideProvider:
		return CloseInfo{Reason: CloseProviderExit, Side: side}
	case SideSSH:
		return CloseInfo{Reason: CloseSessionExit, Side: side}
	default:
		return CloseInfo{Reason: CloseLocalShutdown, Side: side}
	}
}

func classifyError(side Side, err error) CloseInfo {
	switch side {
	case SideProvider:
		return CloseInfo{Reason: CloseProviderError, Side: side, Err: err}
	case SideSSH:
		return CloseInfo{Reason: CloseSSHTransportError, Side: side, Err: err}
	default:
		return CloseInfo{Reason: CloseUnknown, Side: side, Err: err}
	}
}

type LogMetadata struct {
	Provider      string
	Mode          string
	Workspace     string
	TransportImpl string
}

func LogClose(info CloseInfo, metadata LogMetadata) {
	log.Debugw("ssh transport closed",
		"reason", info.Reason, "side", info.Side, "error", info.Err,
		"provider", metadata.Provider, "mode", metadata.Mode,
		"workspace", metadata.Workspace, "transport_impl", metadata.TransportImpl,
	)
}

type RunManagedOptions struct {
	Parent        context.Context
	Conn          ManagedConn
	Handler       func(context.Context) error
	Metadata      LogMetadata
	TransportSide Side
	JoinTimeout   time.Duration
}

type managedOutcome struct {
	firstSide          Side
	parentErr          error
	handlerErr         error
	transportErr       error
	handlerCompleted   bool
	transportCompleted bool
}

func isTeardownOrCancellationError(err error) bool {
	if err == nil {
		return false
	}
	return isCancellationErr(err) || isClosedOrEOFErr(err)
}

func isCancellationErr(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(err.Error(), "context canceled")
}

func isClosedOrEOFErr(err error) bool {
	return isEOFErr(err) || isClosedNetErr(err)
}

func isEOFErr(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, ": EOF") || strings.Contains(msg, "closed pipe")
}

func isClosedNetErr(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "closed network connection") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection is closed")
}

func isExitMissingErr(err error) bool {
	if err == nil {
		return false
	}
	var exitMissing *ssh.ExitMissingError
	if errors.As(err, &exitMissing) {
		return true
	}
	return strings.Contains(err.Error(), "remote command exited without exit status or exit signal")
}

func isTransportTeardownError(err error) bool {
	if err == nil {
		return true
	}
	return isTeardownOrCancellationError(err) || isExitMissingErr(err)
}

func isMeaningfulHandlerErr(outcome managedOutcome) bool {
	return outcome.handlerCompleted && outcome.handlerErr != nil &&
		!isTeardownOrCancellationError(outcome.handlerErr)
}

func isGenuineProviderFailure(outcome managedOutcome) bool {
	if !outcome.transportCompleted || outcome.transportErr == nil {
		return false
	}
	return outcome.firstSide == SideProvider && !isTransportTeardownError(outcome.transportErr)
}

func resolveManagedErrors(outcome managedOutcome) error {
	if outcome.firstSide == SideParent {
		return outcome.parentErr
	}
	if isMeaningfulHandlerErr(outcome) {
		return outcome.handlerErr
	}
	if isGenuineProviderFailure(outcome) {
		return outcome.transportErr
	}
	if outcome.handlerCompleted && outcome.handlerErr == nil {
		return nil
	}
	if err := resolveParentCancellation(outcome); err != nil {
		return err
	}
	return resolveByFirstSide(outcome)
}

func resolveByFirstSide(outcome managedOutcome) error {
	switch outcome.firstSide {
	case SideSSH:
		return outcome.handlerErr
	case SideProvider:
		return resolveProviderFirst(outcome)
	default:
		return resolveFallback(outcome)
	}
}

func resolveParentCancellation(outcome managedOutcome) error {
	if outcome.parentErr != nil &&
		(errors.Is(outcome.parentErr, context.Canceled) || errors.Is(outcome.parentErr, context.DeadlineExceeded)) {
		if !outcome.handlerCompleted || isTeardownOrCancellationError(outcome.handlerErr) {
			return outcome.parentErr
		}
	}
	return nil
}

func resolveProviderFirst(outcome managedOutcome) error {
	if !outcome.handlerCompleted || isTeardownOrCancellationError(outcome.handlerErr) {
		return outcome.transportErr
	}
	return outcome.handlerErr
}

func resolveFallback(outcome managedOutcome) error {
	if outcome.handlerCompleted && outcome.handlerErr != nil {
		return outcome.handlerErr
	}
	if outcome.transportCompleted && outcome.transportErr != nil {
		return outcome.transportErr
	}
	return outcome.parentErr
}

func waitForFirst(
	parent context.Context,
	transportSide Side,
	handlerDone <-chan error,
	connDone <-chan error,
) (managedOutcome, error) {
	var outcome managedOutcome
	select {
	case err := <-connDone:
		outcome.firstSide = transportSide
		outcome.transportErr = err
		outcome.transportCompleted = true
		return outcome, err
	case err := <-handlerDone:
		outcome.firstSide = SideSSH
		outcome.handlerErr = err
		outcome.handlerCompleted = true
		return outcome, err
	case <-parent.Done():
		outcome.firstSide = SideParent
		return outcome, parent.Err()
	}
}

func initiateTeardown(conn ManagedConn, firstSide Side, handlerErr error) {
	if firstSide == SideSSH && handlerErr == nil {
		if cw, ok := conn.(CloseWriter); ok {
			if err := cw.CloseWrite(); err == nil {
				return
			}
		}
	}
	_ = conn.Close()
}

func joinRemaining(
	joinCtx context.Context,
	handlerDone <-chan error,
	connDone <-chan error,
	outcome *managedOutcome,
) {
	for !outcome.handlerCompleted || !outcome.transportCompleted {
		select {
		case err := <-handlerDone:
			outcome.handlerErr = err
			outcome.handlerCompleted = true
		case err := <-connDone:
			outcome.transportErr = err
			outcome.transportCompleted = true
		case <-joinCtx.Done():
			return
		}
	}
}

func RunManaged(opts RunManagedOptions) error {
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.Conn == nil {
		return errors.New("managed connection is required")
	}
	if opts.Handler == nil {
		return errors.New("handler is required")
	}

	joinTimeout := opts.JoinTimeout
	if joinTimeout <= 0 {
		joinTimeout = DefaultJoinTimeout
	}

	lifecycle, ctx := NewPersistentLifecycle(opts.Parent)
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- opts.Handler(ctx) }()
	connDone := make(chan error, 1)
	go func() { connDone <- opts.Conn.Wait() }()

	outcome, firstErr := waitForFirst(opts.Parent, opts.TransportSide, handlerDone, connDone)
	lifecycle.Close(Classify(outcome.firstSide, firstErr, opts.Parent))

	initiateTeardown(opts.Conn, outcome.firstSide, outcome.handlerErr)
	joinCtx, cancelJoin := context.WithTimeout(context.WithoutCancel(opts.Parent), joinTimeout)
	defer cancelJoin()
	joinRemaining(joinCtx, handlerDone, connDone, &outcome)

	_ = opts.Conn.Close()
	LogClose(lifecycle.CloseInfo(), opts.Metadata)
	outcome.parentErr = opts.Parent.Err()
	return resolveManagedErrors(outcome)
}
