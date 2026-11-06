package tunnel

import (
	"context"
	"io"
	"time"
)

const (
	LifecycleTerminalProtocolVersion  uint32 = 1
	LifecycleTerminalMaxFrame                = 32 << 10
	LifecycleTerminalHandshakeTimeout        = 5 * time.Second
	LifecycleTerminalExitAckTimeout          = 30 * time.Second
)

type TerminalWindow struct {
	Term          string
	Rows, Columns uint16
}

type TerminalInvocation struct {
	ID, Phase, Key string
	Index          int
}

type TerminalExit struct {
	ExitCode           int
	Signal, StartError string
}

type TerminalSession interface {
	Begin(ctx context.Context, inv TerminalInvocation) (TerminalAttachment, error)
	Close(ctx context.Context) error
}

type TerminalAttachment interface {
	Window() TerminalWindow
	Output() io.Writer
	Input() io.ReadCloser
	Resizes() <-chan TerminalWindow
	Canceled() <-chan struct{}
	Exit(ctx context.Context, exit TerminalExit) error
}
