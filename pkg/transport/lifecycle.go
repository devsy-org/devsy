package transport

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
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
	lifecycle, ctx := NewPersistentLifecycle(opts.Parent)
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- opts.Handler(ctx) }()
	connDone := make(chan error, 1)
	go func() { connDone <- opts.Conn.Wait() }()

	var info CloseInfo
	select {
	case err := <-connDone:
		info = Classify(opts.TransportSide, err, opts.Parent)
	case err := <-handlerDone:
		info = Classify(SideSSH, err, opts.Parent)
	case <-opts.Parent.Done():
		info = Classify(SideParent, opts.Parent.Err(), opts.Parent)
	}
	lifecycle.Close(info)
	_ = opts.Conn.Close()
	LogClose(lifecycle.CloseInfo(), opts.Metadata)
	return lifecycle.CloseInfo().Err
}
