package transport

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
)

type CloseReason string

type TransportCloseReason = CloseReason

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

type TransportSide = Side

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

type TransportCloseInfo = CloseInfo

type PersistentLifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	mu     sync.RWMutex
	info   CloseInfo
}

func NewPersistentLifecycle(parent context.Context) *PersistentLifecycle {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &PersistentLifecycle{
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		info: CloseInfo{Reason: CloseUnknown, Side: SideUnknown},
	}
}

type PersistentTransport = PersistentLifecycle

func NewPersistentTransport(parent context.Context) *PersistentLifecycle {
	return NewPersistentLifecycle(parent)
}

func (l *PersistentLifecycle) Context() context.Context { return l.ctx }
func (l *PersistentLifecycle) Done() <-chan struct{}    { return l.done }

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
		switch side {
		case SideProvider:
			return CloseInfo{Reason: CloseProviderExit, Side: side}
		case SideSSH:
			return CloseInfo{Reason: CloseSessionExit, Side: side}
		default:
			return CloseInfo{Reason: CloseLocalShutdown, Side: side}
		}
	}

	if errors.Is(err, io.EOF) {
		return CloseInfo{Reason: ClosePeerEOF, Side: side, Err: err}
	}
	switch side {
	case SideProvider:
		return CloseInfo{Reason: CloseProviderError, Side: side, Err: err}
	case SideSSH:
		return CloseInfo{Reason: CloseSSHTransportError, Side: side, Err: err}
	default:
		return CloseInfo{Reason: CloseUnknown, Side: side, Err: err}
	}
}

func ClassifyTransportError(side Side, err error, parent context.Context) CloseInfo {
	return Classify(side, err, parent)
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

type TransportLogMetadata = LogMetadata

func LogTransportClose(info CloseInfo, metadata LogMetadata) {
	LogClose(info, metadata)
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
	lifecycle := NewPersistentLifecycle(opts.Parent)
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- opts.Handler(lifecycle.Context()) }()
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
