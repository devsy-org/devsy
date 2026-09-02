package tunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
)

// TransportCloseReason describes why a persistent transport ended. The reason
// is recorded by the transport owner before any shared-resource cleanup runs.
type TransportCloseReason string

const (
	CloseUnknown           TransportCloseReason = "unknown"
	ClosePeerEOF           TransportCloseReason = "peer_eof"
	CloseContextCancelled  TransportCloseReason = "context_cancelled"
	CloseLocalShutdown     TransportCloseReason = "local_shutdown"
	CloseProviderExit      TransportCloseReason = "provider_exit"
	CloseProviderError     TransportCloseReason = "provider_error"
	CloseSSHTransportError TransportCloseReason = "ssh_transport_error"
	CloseSessionExit       TransportCloseReason = "session_exit"
	CloseKeepaliveTimeout  TransportCloseReason = "keepalive_timeout"
)

// TransportSide identifies the participant that first caused transport
// shutdown.
type TransportSide string

const (
	TransportSideUnknown  TransportSide = "unknown"
	TransportSideProvider TransportSide = "provider"
	TransportSideSSH      TransportSide = "ssh"
	TransportSideParent   TransportSide = "parent"
)

// TransportCloseInfo is the immutable, first-close-wins result of a
// persistent transport's lifecycle. Err is the initiating error, not a later
// cleanup error caused by shutting down the other side.
type TransportCloseInfo struct {
	Reason TransportCloseReason
	Err    error
	Side   TransportSide
}

// PersistentTransport is retained as a compatibility alias for
// transport.PersistentLifecycle. New persistent transports should use the
// managed connection APIs directly.
type PersistentTransport struct {
	ctx    context.Context
	cancel context.CancelFunc

	done chan struct{}
	once sync.Once

	mu   sync.RWMutex
	info TransportCloseInfo
}

// NewPersistentTransport creates a persistent transport whose context is a
// child of parent. Parent cancellation propagates to Context immediately.
func NewPersistentTransport(parent context.Context) *PersistentTransport {
	ctx, cancel := context.WithCancel(parent)
	return &PersistentTransport{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		info:   TransportCloseInfo{Reason: CloseUnknown, Side: TransportSideUnknown},
	}
}

// Context returns the context shared by persistent transport work.
func (t *PersistentTransport) Context() context.Context {
	return t.ctx
}

// Done is closed exactly once when Close records the transport's terminal
// state.
func (t *PersistentTransport) Done() <-chan struct{} {
	return t.done
}

// Close records info and shuts down the transport. The first call wins;
// repeated calls are safe and cannot replace CloseInfo or panic on Done.
func (t *PersistentTransport) Close(info TransportCloseInfo) {
	t.once.Do(func() {
		t.mu.Lock()
		t.info = info
		t.mu.Unlock()

		t.cancel()
		close(t.done)
	})
}

// CloseInfo returns the first recorded close information. Before Close it is
// the zero value, whose Reason is CloseUnknown.
func (t *PersistentTransport) CloseInfo() TransportCloseInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.info
}

// classifyTransportError maps a side completion into the semantic close
// reason for a persistent transport. Parent cancellation takes precedence over
// side errors because cancellation is intentional caller shutdown.
func classifyTransportError(
	side TransportSide,
	err error,
	parentCtx context.Context,
) TransportCloseInfo {
	if parentCtx.Err() != nil {
		return TransportCloseInfo{
			Reason: CloseContextCancelled,
			Side:   TransportSideParent,
			Err:    parentCtx.Err(),
		}
	}

	if err == nil {
		return TransportCloseInfo{
			Reason: CloseLocalShutdown,
			Side:   side,
		}
	}

	if errors.Is(err, io.EOF) {
		return TransportCloseInfo{
			Reason: ClosePeerEOF,
			Side:   side,
			Err:    err,
		}
	}

	switch side {
	case TransportSideProvider:
		return TransportCloseInfo{Reason: CloseProviderError, Side: side, Err: err}
	case TransportSideSSH:
		return TransportCloseInfo{Reason: CloseSSHTransportError, Side: side, Err: err}
	default:
		return TransportCloseInfo{Reason: CloseUnknown, Side: side, Err: err}
	}
}

// TransportLogMetadata supplies optional context for the canonical close log.
type TransportLogMetadata struct {
	Provider  string
	Mode      string
	Workspace string
}

// LogTransportClose emits the single canonical close event for a persistent
// SSH transport. Cleanup diagnostics remain separate from this event.
func LogTransportClose(info TransportCloseInfo, metadata TransportLogMetadata) {
	log.Debugw("ssh transport closed",
		"reason", info.Reason,
		"side", info.Side,
		"error", info.Err,
		"provider", metadata.Provider,
		"mode", metadata.Mode,
		"workspace", metadata.Workspace,
	)
}

// runPersistentPair gives a PipeBridge pair an explicit persistent lifecycle
// owner without changing PipeBridge's byte movement or join semantics.
func runPersistentPair(
	parent context.Context,
	pb *PipeBridge,
	tunnelFn TunnelFunc,
	handlerFn HandlerFunc,
) (TransportCloseInfo, error) {
	transport := NewPersistentTransport(parent)

	go func() {
		select {
		case <-parent.Done():
			transport.Close(classifyTransportError(TransportSideParent, parent.Err(), parent))
		case <-transport.Done():
		}
	}()

	err := pb.RunPair(
		transport.Context(),
		func(ctx context.Context, stdin, stdout *os.File) error {
			err := tunnelFn(ctx, stdin, stdout)
			transport.Close(classifyTransportError(TransportSideProvider, err, parent))
			return err
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			err := handlerFn(ctx, stdout, stdin)
			transport.Close(classifyTransportError(TransportSideSSH, err, parent))
			return err
		},
	)

	select {
	case <-transport.Done():
	default:
		transport.Close(classifyTransportError(TransportSideUnknown, err, parent))
	}
	return transport.CloseInfo(), err
}
